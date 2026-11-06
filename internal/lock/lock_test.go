package lock_test

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/axonops/axai/internal/lock"
)

func TestLockBusy(t *testing.T) {
	dir := t.TempDir()
	l, err := lock.Acquire(dir)
	require.NoError(t, err)

	_, err = lock.Acquire(dir)
	var be *lock.BusyError
	require.ErrorAs(t, err, &be)
	require.Equal(t, strconv.Itoa(os.Getpid()), be.PID)
	require.Contains(t, err.Error(), be.PID)

	require.NoError(t, l.Release())
	again, err := lock.Acquire(dir)
	require.NoError(t, err)
	require.NoError(t, again.Release())
}
