package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
)

var ErrWritableFS = errors.New("storage does not support writes")

type Storage interface {
	fs.FS
	Get(path string) ([]byte, error)
	Exists(path string) bool
	List(path string) []string
	FilePath(path string) (fpath string, ok bool)
}

type WritableFS interface {
	fs.FS
	OpenWriter(name string, flag int) (io.WriteCloser, error)
	Rename(oldName, newName string) error
	Remove(name string) error
}

func writePage(storage Storage, path string, data []byte) error {
	writable, ok := storage.(WritableFS)
	if !ok {
		return ErrWritableFS
	}

	name, err := normalizeFSPath(path)
	if err != nil {
		return err
	}
	writer, err := writable.OpenWriter(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return err
	}
	_, writeErr := io.Copy(writer, bytes.NewReader(data))
	closeErr := writer.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
