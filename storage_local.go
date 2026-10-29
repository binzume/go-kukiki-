package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type LocalStorage struct {
	fs.FS
	base string
}

func NewLocalStorage(path string) *LocalStorage {
	return &LocalStorage{FS: os.DirFS(path), base: path}
}

func (s *LocalStorage) OpenWriter(name string, flag int) (io.WriteCloser, error) {
	path, err := s.resolve(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if flag&os.O_CREATE != 0 {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, flag, 0644)
}

func (s *LocalStorage) Rename(oldName, newName string) error {
	oldPath, err := s.resolve(oldName)
	if err != nil {
		return err
	}
	newPath, err := s.resolve(newName)
	if err != nil {
		return err
	}
	return os.Rename(oldPath, newPath)
}

func (s *LocalStorage) Remove(name string) error {
	path, err := s.resolve(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (s *LocalStorage) resolve(path string) (string, error) {
	path, err := normalizeFSPath(path)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.base, filepath.FromSlash(path)), nil
}
