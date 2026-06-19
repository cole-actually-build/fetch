package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

var _ Store = (*Disk)(nil)

// Disk is a content-addressed artifact store rooted at a directory.
type Disk struct {
	root string
}

func NewDisk(root string) *Disk { return &Disk{root: root} }

func (d *Disk) Put(ctx context.Context, runID, stepID string, data []byte, ext string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:]) + "." + ext
	rel := filepath.Join(runID, stepID, name)
	full := filepath.Join(d.root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

func (d *Disk) Get(ctx context.Context, ref string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(d.root, ref))
}
