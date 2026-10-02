package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"querypro/internal/plugin/pb"
)

const disconnectTimeout = 5 * time.Second

var (
	ErrDisconnected = errors.New("disconnected")
	errExited       = fmt.Errorf("%w: plugin stopped", ErrDisconnected)
)

type session struct {
	p      *proc
	id     string
	server string
}

func rpcErr(err error) error {
	st, ok := status.FromError(err)
	switch {
	case !ok:
		return err
	case st.Code() == codes.Canceled:
		return context.Canceled
	case st.Code() == codes.DeadlineExceeded:
		return context.DeadlineExceeded
	case st.Code() == codes.Unavailable:
		return fmt.Errorf("%w: %s", ErrDisconnected, st.Message())
	}
	return errors.New(st.Message())
}

func (s *session) alive() error {
	select {
	case <-s.p.done:
		return errExited
	default:
		return nil
	}
}

func (s *session) Server() string { return s.server }

func (s *session) Resources(ctx context.Context) ([]Resource, error) {
	if err := s.alive(); err != nil {
		return nil, err
	}
	res, err := s.p.client.Resources(ctx, &pb.ResourcesRequest{Session: s.id})
	if err != nil {
		return nil, rpcErr(err)
	}
	out := make([]Resource, len(res.Resources))
	for i, r := range res.Resources {
		out[i] = Resource{Kind: r.Kind, Name: r.Name}
	}
	return out, nil
}

func (s *session) Actions(ctx context.Context, r Resource) ([]Action, error) {
	if err := s.alive(); err != nil {
		return nil, err
	}
	res, err := s.p.client.Actions(ctx, &pb.ActionsRequest{
		Session:  s.id,
		Resource: &pb.Resource{Kind: r.Kind, Name: r.Name},
	})
	if err != nil {
		return nil, rpcErr(err)
	}
	out := make([]Action, len(res.Actions))
	for i, a := range res.Actions {
		out[i] = Action{Name: a.Name, Query: a.Query, Danger: a.Danger}
		for _, p := range a.Params {
			out[i].Params = append(out[i].Params, Param{Name: p.Name, Value: p.Value})
		}
	}
	return out, nil
}

func (s *session) Build(ctx context.Context, r Resource, action string, values []string) (string, error) {
	if err := s.alive(); err != nil {
		return "", err
	}
	res, err := s.p.client.Build(ctx, &pb.BuildRequest{
		Session:  s.id,
		Resource: &pb.Resource{Kind: r.Kind, Name: r.Name},
		Action:   action,
		Values:   values,
	})
	if err != nil {
		return "", rpcErr(err)
	}
	return res.Query, nil
}

func (s *session) Query(ctx context.Context, q string) (Result, error) {
	if err := s.alive(); err != nil {
		return Result{}, err
	}
	stream, err := s.p.client.Query(ctx, &pb.QueryRequest{Session: s.id, Query: q})
	if err != nil {
		return Result{}, rpcErr(err)
	}
	ev, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return Result{Summary: "done"}, nil
	}
	if err != nil {
		return Result{}, rpcErr(err)
	}
	switch e := ev.Event.(type) {
	case *pb.QueryResponse_Table:
		rows := make([][]string, len(e.Table.Rows))
		for i, r := range e.Table.Rows {
			rows[i] = r.Cells
		}
		return Result{Columns: e.Table.Columns, Rows: rows, Summary: ev.Summary}, nil
	case *pb.QueryResponse_Text:
		return Result{Text: e.Text, Summary: ev.Summary}, nil
	case *pb.QueryResponse_Live:
		ch := make(chan string)
		var serr error
		go func() {
			defer close(ch)
			for {
				ev, err := stream.Recv()
				if err != nil {
					if !errors.Is(err, io.EOF) && ctx.Err() == nil {
						serr = rpcErr(err)
					}
					return
				}
				select {
				case ch <- ev.GetLine():
				case <-ctx.Done():
					return
				}
			}
		}()
		return Result{Stream: ch, Summary: ev.Summary, Err: func() error { return serr }}, nil
	}
	return Result{}, errors.New("plugin sent an unknown first event")
}

func (s *session) Close() error {
	if s.alive() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), disconnectTimeout)
	defer cancel()
	_, err := s.p.client.Disconnect(ctx, &pb.DisconnectRequest{Session: s.id})
	return rpcErr(err)
}
