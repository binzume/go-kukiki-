package main

type Storage interface {
	Get(path string) ([]byte, error)
	Store(path string, data []byte) error
	Exists(path string) bool
	List(path string) []string
	FilePath(path string) (fpath string, ok bool)
}
