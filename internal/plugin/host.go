package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"querypro/internal/plugin/pb"
)

const (
	Handshake    = "querypro-plugin 1"
	startTimeout = 20 * time.Second
	stopTimeout  = 3 * time.Second
	maxMessage   = 64 << 20
	maxSockPath  = 100
	maxHandshake = 4096
	maxLog       = 10 << 20
)

type Exit struct {
	Kind string
	Err  error
}

type Host struct {
	kinds  []Kind
	logDir string
	sock   string
	exits  chan Exit

	mu     sync.Mutex
	procs  map[string]*proc
	closed bool
}

type proc struct {
	ready  chan struct{}
	done   chan struct{}
	err    error
	cmd    *exec.Cmd
	stdin  io.Closer
	conn   *grpc.ClientConn
	client pb.PluginServiceClient
}

func Discover(dir string) ([]Kind, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*", "plugin.json"))
	if err != nil {
		return nil, err
	}
	var kinds []Kind
	seen := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var k Kind
		if err := json.Unmarshal(b, &k); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if k.Name == "" || k.Code == "" || len(k.Command) == 0 {
			return nil, fmt.Errorf("%s: kind, code and command are required", p)
		}
		if prev, ok := seen[k.Name]; ok {
			return nil, fmt.Errorf("%s: kind %q is already provided by %s", p, k.Name, prev)
		}
		seen[k.Name] = p
		k.Dir = filepath.Dir(p)
		kinds = append(kinds, k)
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no plugins found in %s", dir)
	}
	return kinds, nil
}

func NewHost(dir, logDir string) (*Host, error) {
	kinds, err := Discover(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, err
	}
	sock, err := os.MkdirTemp("", "qp-")
	if err == nil && len(sock) > maxSockPath-20 {
		_ = os.Remove(sock)
		sock, err = os.MkdirTemp("/tmp", "qp-")
	}
	if err != nil {
		return nil, err
	}
	return &Host{
		kinds:  kinds,
		logDir: logDir,
		sock:   sock,
		exits:  make(chan Exit, 16),
		procs:  map[string]*proc{},
	}, nil
}

func (h *Host) Kinds() []Kind { return h.kinds }

func (h *Host) Exits() <-chan Exit { return h.exits }

func (h *Host) Connect(ctx context.Context, kind, uri string) (Session, error) {
	p, err := h.proc(ctx, kind)
	if err != nil {
		return nil, err
	}
	res, err := p.client.Connect(ctx, &pb.ConnectRequest{Uri: uri})
	if err != nil {
		return nil, rpcErr(err)
	}
	return &session{p: p, id: res.Session, server: res.Server}, nil
}

func (h *Host) Close() {
	h.mu.Lock()
	h.closed = true
	procs := make([]*proc, 0, len(h.procs))
	for _, p := range h.procs {
		procs = append(procs, p)
	}
	h.mu.Unlock()
	for _, p := range procs {
		<-p.ready
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
	}
	for _, p := range procs {
		if p.cmd == nil {
			continue
		}
		select {
		case <-p.done:
		case <-time.After(stopTimeout):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	}
	_ = os.RemoveAll(h.sock)
}

func (h *Host) proc(ctx context.Context, name string) (*proc, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("plugin host is closed")
	}
	p := h.procs[name]
	if p == nil {
		var kind *Kind
		for i := range h.kinds {
			if h.kinds[i].Name == name {
				kind = &h.kinds[i]
			}
		}
		if kind == nil {
			h.mu.Unlock()
			return nil, fmt.Errorf("no plugin for %q", name)
		}
		p = &proc{ready: make(chan struct{}), done: make(chan struct{})}
		h.procs[name] = p
		go h.start(*kind, p)
	}
	h.mu.Unlock()
	select {
	case <-p.ready:
		return p, p.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *Host) start(k Kind, p *proc) {
	p.err = h.spawn(k, p)
	if p.err != nil {
		h.forget(k.Name, p)
	}
	close(p.ready)
}

func (h *Host) forget(name string, p *proc) {
	h.mu.Lock()
	if h.procs[name] == p {
		delete(h.procs, name)
	}
	h.mu.Unlock()
}

func (h *Host) spawn(k Kind, p *proc) error {
	logPath := filepath.Join(h.logDir, k.Name+".log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > maxLog {
		flags |= os.O_TRUNC
	}
	logf, err := os.OpenFile(logPath, flags, 0o600)
	if err != nil {
		return err
	}
	sock := filepath.Join(h.sock, k.Name+".sock")
	_ = os.Remove(sock)

	hs := &handshake{line: make(chan string, 1), log: logf}
	cmd := exec.Command(k.Command[0], k.Command[1:]...)
	cmd.Dir = k.Dir
	cmd.Env = append(os.Environ(), "QUERYPRO_SOCKET="+sock)
	cmd.Stdout = hs
	cmd.Stderr = logf
	cmd.WaitDelay = stopTimeout
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = logf.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return fmt.Errorf("start %s plugin: %w", k.Name, err)
	}
	p.cmd, p.stdin = cmd, stdin
	go h.wait(k.Name, p, logf)

	fail := func(err error) error {
		_ = cmd.Process.Kill()
		return fmt.Errorf("%s plugin: %w (log: %s)", k.Name, err, logPath)
	}
	select {
	case line := <-hs.line:
		if line != Handshake {
			return fail(fmt.Errorf("unexpected handshake %q", line))
		}
	case <-p.done:
		return fail(errors.New("exited during startup"))
	case <-time.After(startTimeout):
		return fail(errors.New("no handshake within " + startTimeout.String()))
	}
	conn, err := grpc.NewClient("unix://"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMessage)),
	)
	if err != nil {
		return fail(err)
	}
	p.conn, p.client = conn, pb.NewPluginServiceClient(conn)
	return nil
}

func (h *Host) wait(name string, p *proc, logf *os.File) {
	err := p.cmd.Wait()
	_ = logf.Close()
	close(p.done)
	<-p.ready
	h.forget(name, p)
	if p.conn != nil {
		_ = p.conn.Close()
	}
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if p.err != nil || closed {
		return
	}
	if err == nil {
		err = errors.New("exited")
	}
	select {
	case h.exits <- Exit{Kind: name, Err: fmt.Errorf("%s plugin stopped: %w", name, err)}:
	default:
	}
}

type handshake struct {
	buf  []byte
	sent bool
	line chan string
	log  io.Writer
}

func (w *handshake) Write(b []byte) (int, error) {
	if w.sent {
		return w.log.Write(b)
	}
	w.buf = append(w.buf, b...)
	i := bytes.IndexByte(w.buf, '\n')
	if i < 0 && len(w.buf) < maxHandshake {
		return len(b), nil
	}
	if i < 0 {
		i = len(w.buf)
	}
	w.sent = true
	w.line <- string(w.buf[:i])
	_, err := w.log.Write(w.buf[min(i+1, len(w.buf)):])
	w.buf = nil
	return len(b), err
}
