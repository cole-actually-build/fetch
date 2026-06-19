// Package artifacts stores raw fetched bytes so extraction can be re-run
// without re-fetching. Refs are paths relative to the store root.
package artifacts

import "context"

// Store persists and retrieves raw artifact bytes.
type Store interface {
	Put(ctx context.Context, runID, stepID string, data []byte, ext string) (ref string, err error)
	Get(ctx context.Context, ref string) ([]byte, error)
}
