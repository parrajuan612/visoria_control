// Package workspace implementa ports.Workspace sobre el disco.
package workspace

import (
	"os"

	"visoria-control/internal/ports"
)

type Disk struct{ base string }

var _ ports.Workspace = Disk{}

// New usa base como carpeta contenedora ("" = temporal del sistema).
func New(base string) Disk { return Disk{base: base} }

func (d Disk) Open() (string, func(), error) {
	if d.base != "" {
		if err := os.MkdirAll(d.base, 0o750); err != nil {
			return "", nil, err
		}
	}
	dir, err := os.MkdirTemp(d.base, "docs-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}

func (Disk) WriteFile(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
