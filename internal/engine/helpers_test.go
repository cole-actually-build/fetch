package engine

import (
	"testing"

	"github.com/cole/fetch/internal/pipeline"
)

func newTempRepo(t *testing.T) *pipeline.Repository {
	t.Helper()
	return pipeline.NewRepository(t.TempDir())
}
