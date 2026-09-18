package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type Store struct {
	dir string
}

func Dir() (string, error) {
	if d := os.Getenv("QUERYPRO_HOME"); d != "" {
		return d, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "querypro"), nil
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Path(name string) string { return filepath.Join(s.dir, name) }

func (s *Store) Load(name string, v any) error {
	b, err := os.ReadFile(s.Path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s is corrupt, fix or remove it: %w", s.Path(name), err)
	}
	return nil
}

func (s *Store) Save(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, name+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), s.Path(name))
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}
