package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var sectionHeaderPattern = regexp.MustCompile(`^&\[([A-Za-z0-9_.-]+)(?:#([0-9]+))?\][ \t]*$`)

var (
	ErrSectionNotFound = errors.New("section not found")
)

type Section struct {
	Name         string
	UpdateUnixMS int64
	Body         string
}

func validSectionName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func parseSectionHeader(line string) (Section, bool, error) {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	match := sectionHeaderPattern.FindStringSubmatch(line)
	if match == nil {
		return Section{}, false, nil
	}

	section := Section{Name: match[1]}
	if match[2] != "" {
		value, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			return Section{}, false, fmt.Errorf("invalid section update timestamp: %w", err)
		}
		section.UpdateUnixMS = value
	}
	return section, true, nil
}

func readSection(storage fs.FS, path, name string) (Section, error) {
	if !validSectionName(name) {
		return Section{}, fmt.Errorf("invalid section name: %q", name)
	}

	storageName, err := normalizeFSPath(path)
	if err != nil {
		return Section{}, err
	}
	file, err := storage.Open(storageName)
	if err != nil {
		return Section{}, err
	}
	defer file.Close()

	return scanSection(file, name)
}

func scanSection(reader io.Reader, name string) (Section, error) {
	scanner := bufio.NewScanner(reader)
	section := Section{}
	found := false
	var body strings.Builder

	for scanner.Scan() {
		line := scanner.Text() + "\n"
		candidate, ok, err := parseSectionHeader(line)
		if err != nil {
			return Section{}, err
		}
		if ok {
			if found {
				break
			}
			if candidate.Name == name {
				section = candidate
				found = true
			}
			continue
		}

		if found {
			body.WriteString(line)
		}
	}
	if err := scanner.Err(); err != nil {
		return Section{}, err
	}
	if !found {
		return Section{}, ErrSectionNotFound
	}
	section.Body = body.String()
	return section, nil
}

func updateSection(storage fs.FS, path, name, body string, updateUnixMS int64, prepend bool) error {
	if !validSectionName(name) {
		return fmt.Errorf("invalid section name: %q", name)
	}
	if updateUnixMS < 0 {
		return fmt.Errorf("invalid section update timestamp: %d", updateUnixMS)
	}
	writable, ok := storage.(WritableFS)
	if !ok {
		return ErrWritableFS
	}

	storageName, err := normalizeFSPath(path)
	if err != nil {
		return err
	}
	if body == "" {
		return deleteSection(writable, storageName, name)
	}
	temporaryName, temporary, err := openTemporaryWriter(writable, storageName)
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = writable.Remove(temporaryName)
		}
	}()

	source, err := storage.Open(storageName)
	var rewriteErr error
	var sourceErr error
	if err != nil {
		if !os.IsNotExist(err) {
			_ = temporary.Close()
			return err
		}
		rewriteErr = writeNewSection(temporary, name, body, updateUnixMS)
	} else {
		rewriteErr = rewriteSection(source, temporary, name, body, updateUnixMS)
		sourceErr = source.Close()
	}
	if errors.Is(rewriteErr, ErrSectionNotFound) {
		// rewriteSection has copied the original file to the temporary file.
		if prepend {
			if err := temporary.Close(); err != nil {
				return err
			}

			// The section was missing, so create another temporary file with
			// the new section before the already-copied original contents.
			prependedName, prepended, err := openTemporaryWriter(writable, storageName)
			if err != nil {
				return err
			}
			removePrepended := true
			defer func() {
				if removePrepended {
					_ = writable.Remove(prependedName)
				}
			}()

			prependErr := writeNewSection(prepended, name, body, updateUnixMS)
			copied, copyErr := writable.Open(temporaryName)
			if copyErr == nil {
				_, copyErr = io.Copy(prepended, copied)
				closeErr := copied.Close()
				if copyErr == nil {
					copyErr = closeErr
				}
			}
			closeErr := prepended.Close()
			if prependErr != nil {
				return prependErr
			}
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if err := writable.Remove(temporaryName); err != nil {
				return err
			}
			if err := writable.Rename(prependedName, storageName); err != nil {
				return err
			}
			removePrepended = false
			removeTemporary = false
			return nil
		}
		rewriteErr = writeNewSection(temporary, name, body, updateUnixMS)
	}
	temporaryErr := temporary.Close()
	if rewriteErr != nil {
		return rewriteErr
	}
	if sourceErr != nil {
		return sourceErr
	}
	if temporaryErr != nil {
		return temporaryErr
	}
	if err := writable.Rename(temporaryName, storageName); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func deleteSection(storage WritableFS, storageName, name string) error {
	source, err := storage.Open(storageName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	temporaryName, temporary, err := openTemporaryWriter(storage, storageName)
	if err != nil {
		_ = source.Close()
		return err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = storage.Remove(temporaryName)
		}
	}()

	rewriteErr := rewriteSection(source, temporary, name, "", 0)
	sourceErr := source.Close()
	temporaryErr := temporary.Close()
	if rewriteErr != nil && !errors.Is(rewriteErr, ErrSectionNotFound) {
		return rewriteErr
	}
	if sourceErr != nil {
		return sourceErr
	}
	if errors.Is(rewriteErr, ErrSectionNotFound) {
		return nil
	}
	if temporaryErr != nil {
		return temporaryErr
	}
	if info, err := fs.Stat(storage, temporaryName); err != nil {
		return err
	} else if info.Size() == 0 {
		if err := storage.Remove(temporaryName); err != nil {
			return err
		}
		if err := storage.Remove(storageName); err != nil {
			return err
		}
		removeTemporary = false
		return nil
	}
	if err := storage.Rename(temporaryName, storageName); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func writeNewSection(destination io.Writer, name, body string, updateUnixMS int64) error {
	if _, err := io.WriteString(destination, formatSectionHeader(name, updateUnixMS)); err != nil {
		return err
	}
	return writeSectionBody(destination, body)
}

func writeSectionBody(destination io.Writer, body string) error {
	if body == "" {
		return nil
	}
	if _, err := io.WriteString(destination, body); err != nil {
		return err
	}
	if !strings.HasSuffix(body, "\n") {
		_, err := io.WriteString(destination, "\n")
		return err
	}
	return nil
}

func openTemporaryWriter(storage WritableFS, original string) (string, io.WriteCloser, error) {
	directory := path.Dir(original)
	base := path.Base(original)
	for attempt := 0; attempt < 10; attempt++ {
		name := path.Join(directory, "."+base+fmt.Sprintf(".kukiki-%d-%d.tmp", os.Getpid(), time.Now().UnixNano()+int64(attempt)))
		writer, err := storage.OpenWriter(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err == nil {
			return name, writer, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("failed to create temporary section file")
}

func rewriteSection(source io.Reader, destination io.Writer, name, body string, updateUnixMS int64) error {
	scanner := bufio.NewScanner(source)
	found := false
	replacing := false

	for scanner.Scan() {
		line := scanner.Text() + "\n"
		candidate, isHeader, err := parseSectionHeader(line)
		if err != nil {
			return err
		}

		if isHeader {
			if replacing {
				if err := writeSectionBody(destination, body); err != nil {
					return err
				}
				replacing = false
			}
			if candidate.Name == name {
				if body != "" {
					if _, err := io.WriteString(destination, formatSectionHeader(name, updateUnixMS)); err != nil {
						return err
					}
				}
				found = true
				replacing = true
				continue
			}
		}

		if !replacing {
			if _, err := io.WriteString(destination, line); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if replacing {
		if err := writeSectionBody(destination, body); err != nil {
			return err
		}
	}
	if !found {
		return ErrSectionNotFound
	}
	return nil
}

func formatSectionHeader(name string, updateUnixMS int64) string {
	if updateUnixMS == 0 {
		return fmt.Sprintf("&[%s]\n", name)
	}
	return fmt.Sprintf("&[%s#%d]\n", name, updateUnixMS)
}
