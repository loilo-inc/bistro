package bistro

import (
	"io"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/loilo-inc/bistro/pkg/internal/teststub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDIKeepsInjectedDependencies(t *testing.T) {
	logger := NewLogger(io.Discard, io.Discard)
	resolver := teststub.NewResolver(t)
	query := teststub.NewQuery(t)
	fss := fstest.MapFS{"fixture": {Data: []byte("injected filesystem")}}
	di := NewDI(logger, resolver, query, fss)
	assert.Same(t, logger, di.Logger)
	assert.Same(t, resolver, di.Ldd)
	assert.Same(t, query, di.Query)
	// FS must be forwarded along with the other dependencies.
	require.NotNil(t, di.FS)
	data, err := fs.ReadFile(di.FS, "fixture")
	require.NoError(t, err)
	assert.Equal(t, "injected filesystem", string(data))
}
