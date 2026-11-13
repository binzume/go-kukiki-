package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var pagePrefixBlacklist = []string{"/api/", "static", "/css/", "/js/"}

func isBlockedPage(page string) bool {
	page = strings.TrimPrefix(page, "/")
	page = strings.TrimSuffix(page, ".md")
	for _, prefix := range pagePrefixBlacklist {
		prefix = strings.Trim(prefix, "/")
		if page == prefix || strings.HasPrefix(page, prefix+"/") {
			return true
		}
	}
	return false
}

// These extensions are blocked by default because browsers can interpret
// their contents as active content. An explicit configuration entry can
// allow a specific extension when the deployment requires it.
var unsafeUploadExtensions = map[string]struct{}{
	".html":  {},
	".htm":   {},
	".xhtml": {},
	".shtml": {},
	".svg":   {},
	".svgz":  {},
	".js":    {},
	".mjs":   {},
	".cjs":   {},
	".css":   {},
	".xsl":   {},
	".xslt":  {},
}

func uploadExtensionAllowed(config *Config, name string) bool {
	base := strings.TrimRight(filepath.Base(name), ". ")
	extension := strings.ToLower(filepath.Ext(base))
	if _, unsafe := unsafeUploadExtensions[extension]; !unsafe {
		return true
	}
	return config != nil && config.AllowUnsafeUploadExtensions
}

func hasQuotedFilename(name string) bool {
	return strings.ContainsAny(name, "'\"")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func firstQueryValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func writeText(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func setLastModifiedFromStorage(w http.ResponseWriter, storage fs.FS, path string) {
	name, err := normalizeFSPath(path)
	if err != nil {
		return
	}
	info, err := fs.Stat(storage, name)
	if err != nil {
		return
	}
	setLastModified(w, info)
}

func apiPagePath(r *http.Request) string {
	page := r.PathValue("page")
	if page == "" {
		return "/"
	}
	return page
}

// registerAPIHandlers attaches the application's API routes to mux.
func registerAPIHandlers(mux *http.ServeMux, storage fs.FS, config *Config) {
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"_status": 200, "message": "It works!"})
	})

	mux.HandleFunc("PUT /api/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		if !isLoggedIn(r) {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

		name, err := normalizeFSPath(r.PathValue("path"))
		if err != nil || isBlockedPage(name) || hasQuotedFilename(name) {
			http.Error(w, "bad file path", http.StatusBadRequest)
			return
		}
		if !uploadExtensionAllowed(config, name) {
			http.Error(w, "unsafe file extension is not allowed", http.StatusBadRequest)
			return
		}
		if r.ContentLength > maxUploadSize {
			http.Error(w, "uploaded file is too large", http.StatusRequestEntityTooLarge)
			return
		}

		size, created, err := writeUploadedFile(storage, name, r.Body)
		if err != nil {
			switch {
			case errors.Is(err, ErrWritableFS):
				http.Error(w, "file uploads are not supported", http.StatusNotImplemented)
			case errors.Is(err, ErrUploadTooLarge):
				http.Error(w, "uploaded file is too large", http.StatusRequestEntityTooLarge)
			case errors.Is(err, ErrNotRegularFile):
				http.Error(w, "target is not a regular file", http.StatusBadRequest)
			default:
				http.Error(w, "failed to store file", http.StatusInternalServerError)
			}
			return
		}

		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{
			"_status": status,
			"path":    name,
			"size":    size,
			"url":     "/" + name,
		})
	})

	mux.HandleFunc("DELETE /api/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		if !isLoggedIn(r) {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

		name, err := normalizeFSPath(r.PathValue("path"))
		if err != nil || isBlockedPage(name) {
			http.Error(w, "bad file path", http.StatusBadRequest)
			return
		}
		if err := removeStoredFile(storage, name); err != nil {
			switch {
			case errors.Is(err, ErrWritableFS):
				http.Error(w, "file deletion is not supported", http.StatusNotImplemented)
			case os.IsNotExist(err):
				http.Error(w, "file not found", http.StatusNotFound)
			case errors.Is(err, ErrNotRegularFile):
				http.Error(w, "target is not a regular file", http.StatusBadRequest)
			default:
				http.Error(w, "failed to delete file", http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/pages/{page...}", func(w http.ResponseWriter, r *http.Request) {
		page, err := pageFile(apiPagePath(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if sectionName, ok := r.URL.Query()["section"]; ok {
			sectionName := firstQueryValue(sectionName)
			section, err := readSection(storage, sectionPage(config, page, sectionName), sectionName)
			if err != nil {
				if errors.Is(err, ErrSectionNotFound) || os.IsNotExist(err) {
					http.Error(w, "section not found", http.StatusNotFound)
					return
				}
				if strings.HasPrefix(err.Error(), "invalid section name:") {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				http.Error(w, "failed to read section", http.StatusInternalServerError)
				return
			}
			w.Header().Set("X-Update-Unix-Ms", strconv.FormatInt(section.UpdateUnixMS, 10))
			writeText(w, http.StatusOK, []byte(section.Body))
			return
		}
		if data, err := readStorageFile(storage, page); err == nil {
			setLastModifiedFromStorage(w, storage, page)
			writeText(w, http.StatusOK, data)
		} else if os.IsNotExist(err) {
			http.Error(w, "page not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to read page", http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("POST /api/pages/{page...}", func(w http.ResponseWriter, r *http.Request) {
		if !isLoggedIn(r) {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		page, err := pageFile(apiPagePath(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if isBlockedPage(page) {
			http.Error(w, "bad page name", http.StatusBadRequest)
			return
		}

		var parsed struct {
			Text         *string `json:"text"`
			UpdateUnixMS int64   `json:"updateUnixMS"`
		}
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&parsed); err != nil || parsed.Text == nil {
			http.Error(w, "request body must contain a text field", http.StatusBadRequest)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			http.Error(w, "request body must contain a single JSON object", http.StatusBadRequest)
			return
		}
		if *parsed.Text != "" && hasQuotedFilename(page) {
			http.Error(w, "file names containing quotes are not allowed", http.StatusBadRequest)
			return
		}
		if sectionValues, ok := r.URL.Query()["section"]; ok {
			sectionName := firstQueryValue(sectionValues)
			targetPage := sectionPage(config, page, sectionName)
			pageConfigPath := "/" + strings.TrimPrefix(targetPage, "/")
			insertInOrder := directoryConfig(config, pageConfigPath).Diary
			if err := saveSection(storage, targetPage, sectionName, *parsed.Text, parsed.UpdateUnixMS, insertInOrder); err != nil {
				switch {
				case errors.Is(err, ErrSectionNotFound), os.IsNotExist(err):
					http.Error(w, "section not found", http.StatusNotFound)
				case errors.Is(err, ErrWritableFS):
					http.Error(w, "section updates are not supported", http.StatusNotImplemented)
				default:
					if strings.HasPrefix(err.Error(), "invalid section name:") || strings.HasPrefix(err.Error(), "invalid section update timestamp:") {
						http.Error(w, err.Error(), http.StatusBadRequest)
					} else {
						http.Error(w, "failed to update section", http.StatusInternalServerError)
					}
				}
				return
			}
			message := "stored"
			if *parsed.Text == "" {
				message = "deleted"
			}
			writeText(w, http.StatusOK, []byte(message))
			return
		}
		if *parsed.Text == "" {
			err := removeStoredFile(storage, page)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				if errors.Is(err, ErrWritableFS) {
					http.Error(w, "page deletion is not supported", http.StatusNotImplemented)
				} else {
					http.Error(w, "failed to delete page", http.StatusInternalServerError)
				}
				return
			}
			writeText(w, http.StatusOK, []byte("deleted"))
			return
		}
		if err := writePage(storage, page, []byte(*parsed.Text)); err != nil {
			if errors.Is(err, ErrWritableFS) {
				http.Error(w, "page updates are not supported", http.StatusNotImplemented)
				return
			}
			http.Error(w, "failed to store page", http.StatusInternalServerError)
			return
		}

		writeText(w, http.StatusOK, []byte("stored"))
	})
}
