package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
)

var ErrWritableFS = errors.New("storage does not support writes")
var ErrUploadTooLarge = errors.New("uploaded file is too large")
var ErrNotRegularFile = errors.New("storage path is not a regular file")

const maxUploadSize int64 = 32 << 20

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

// writeUploadedFile stores an arbitrary file through the writable storage
// abstraction. The temporary file prevents a failed upload from truncating an
// existing file before the new contents have been completely received.
func writeUploadedFile(storage Storage, name string, source io.Reader) (size int64, created bool, err error) {
	writable, ok := storage.(WritableFS)
	if !ok {
		return 0, false, ErrWritableFS
	}

	name, err = normalizeFSPath(name)
	if err != nil {
		return 0, false, err
	}

	info, statErr := fs.Stat(storage, name)
	switch {
	case statErr == nil && !info.Mode().IsRegular():
		return 0, false, ErrNotRegularFile
	case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
		return 0, false, statErr
	}
	created = errors.Is(statErr, fs.ErrNotExist)

	temporaryName, temporary, err := openTemporaryWriter(writable, name)
	if err != nil {
		return 0, false, err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = writable.Remove(temporaryName)
		}
	}()

	limited := io.LimitReader(source, maxUploadSize+1)
	size, err = io.Copy(temporary, limited)
	closeErr := temporary.Close()
	if err != nil {
		return 0, false, err
	}
	if closeErr != nil {
		return 0, false, closeErr
	}
	if size > maxUploadSize {
		return 0, false, ErrUploadTooLarge
	}

	if err := writable.Rename(temporaryName, name); err != nil {
		return 0, false, err
	}
	removeTemporary = false
	return size, created, nil
}

func removeStoredFile(storage Storage, name string) error {
	writable, ok := storage.(WritableFS)
	if !ok {
		return ErrWritableFS
	}

	name, err := normalizeFSPath(name)
	if err != nil {
		return err
	}
	info, err := fs.Stat(storage, name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrNotRegularFile
	}
	return writable.Remove(name)
}
