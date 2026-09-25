package memory

import (
	"testing"

	"github.com/shekhar8352/mini-graph-db/internal/storage"
	"github.com/shekhar8352/mini-graph-db/internal/storage/enginetest"
)

func TestConformance(t *testing.T) {
	enginetest.Run(t, func() storage.Engine { return Open() })
}
