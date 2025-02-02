package dataloadgen_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vikstrous/dataloadgen"
)

func TestUserLoader(t *testing.T) {
	ctx := context.Background()
	var fetches [][]string
	var mu sync.Mutex
	dl := dataloadgen.NewLoader(func(keys []string) ([]*benchmarkUser, []error) {
		mu.Lock()
		fetches = append(fetches, keys)
		mu.Unlock()

		users := make([]*benchmarkUser, len(keys))
		errors := make([]error, len(keys))

		for i, key := range keys {
			if strings.HasPrefix(key, "F") {
				return nil, []error{fmt.Errorf("failed all fetches")}
			}
			if strings.HasPrefix(key, "E") {
				errors[i] = fmt.Errorf("user not found")
			} else {
				users[i] = &benchmarkUser{ID: key, Name: "user " + key}
			}
		}
		return users, errors
	},
		dataloadgen.WithBatchCapacity(5),
		dataloadgen.WithWait(10*time.Millisecond),
	)

	t.Run("fetch concurrent data", func(t *testing.T) {
		t.Run("load user successfully", func(t *testing.T) {
			t.Parallel()
			u, err := dl.Load(ctx, "U1")
			require.NoError(t, err)
			require.Equal(t, u.ID, "U1")
		})

		t.Run("load failed user", func(t *testing.T) {
			t.Parallel()
			u, err := dl.Load(ctx, "E1")
			require.Error(t, err)
			require.Nil(t, u)
		})

		t.Run("load many users", func(t *testing.T) {
			t.Parallel()
			u, err := dl.LoadAll(ctx, []string{"U2", "E2", "E3", "U4"})
			require.Equal(t, u[0].Name, "user U2")
			require.Equal(t, u[3].Name, "user U4")
			require.Error(t, err[1])
			require.Error(t, err[2])
		})

		t.Run("load thunk", func(t *testing.T) {
			t.Parallel()
			thunk1 := dl.LoadThunk(ctx, "U5")
			thunk2 := dl.LoadThunk(ctx, "E5")

			u1, err1 := thunk1()
			require.NoError(t, err1)
			require.Equal(t, "user U5", u1.Name)

			u2, err2 := thunk2()
			require.Error(t, err2)
			require.Nil(t, u2)
		})
	})

	t.Run("it sent two batches", func(t *testing.T) {
		mu.Lock()
		defer mu.Unlock()

		require.Len(t, fetches, 2)
		assert.Len(t, fetches[0], 5)
		assert.Len(t, fetches[1], 3)
	})

	t.Run("fetch more", func(t *testing.T) {

		t.Run("previously cached", func(t *testing.T) {
			t.Parallel()
			u, err := dl.Load(ctx, "U1")
			require.NoError(t, err)
			require.Equal(t, u.ID, "U1")
		})

		t.Run("load many users", func(t *testing.T) {
			t.Parallel()
			u, err := dl.LoadAll(ctx, []string{"U2", "U4"})
			require.Len(t, err, 0)
			require.Equal(t, u[0].Name, "user U2")
			require.Equal(t, u[1].Name, "user U4")
		})
	})

	t.Run("no round trips", func(t *testing.T) {
		mu.Lock()
		defer mu.Unlock()

		require.Len(t, fetches, 2)
	})

	t.Run("fetch partial", func(t *testing.T) {
		t.Run("errors not in cache cache value", func(t *testing.T) {
			t.Parallel()
			u, err := dl.Load(ctx, "E2")
			require.Nil(t, u)
			require.Error(t, err)
		})

		t.Run("load all", func(t *testing.T) {
			t.Parallel()
			u, err := dl.LoadAll(ctx, []string{"U1", "U4", "E1", "U9", "U5"})
			require.Equal(t, u[0].ID, "U1")
			require.Equal(t, u[1].ID, "U4")
			require.Error(t, err[2])
			require.Equal(t, u[3].ID, "U9")
			require.Equal(t, u[4].ID, "U5")
		})
	})

	t.Run("one partial trip", func(t *testing.T) {
		mu.Lock()
		defer mu.Unlock()

		require.Len(t, fetches, 3)
		require.Len(t, fetches[2], 1) // U9 only because E1 and E2 are already cached as failed and only U9 is new
	})

	t.Run("primed reads dont hit the fetcher", func(t *testing.T) {
		dl.Prime("U99", &benchmarkUser{ID: "U99", Name: "Primed user"})
		u, err := dl.Load(ctx, "U99")
		require.NoError(t, err)
		require.Equal(t, "Primed user", u.Name)

		require.Len(t, fetches, 3)
	})

	t.Run("priming in a loop is safe", func(t *testing.T) {
		users := []benchmarkUser{
			{ID: "Alpha", Name: "Alpha"},
			{ID: "Omega", Name: "Omega"},
		}
		for _, user := range users {
			user := user
			dl.Prime(user.ID, &user)
		}

		u, err := dl.Load(ctx, "Alpha")
		require.NoError(t, err)
		require.Equal(t, "Alpha", u.Name)

		u, err = dl.Load(ctx, "Omega")
		require.NoError(t, err)
		require.Equal(t, "Omega", u.Name)

		require.Len(t, fetches, 3)
	})

	t.Run("cleared results will go back to the fetcher", func(t *testing.T) {
		dl.Clear("U99")
		u, err := dl.Load(ctx, "U99")
		require.NoError(t, err)
		require.Equal(t, "user U99", u.Name)

		require.Len(t, fetches, 4)
	})

	t.Run("load all thunk", func(t *testing.T) {
		thunk1 := dl.LoadAllThunk(ctx, []string{"U5", "U6"})
		thunk2 := dl.LoadAllThunk(ctx, []string{"U6", "E6"})

		users1, err1 := thunk1()
		require.Len(t, fetches, 5)

		require.NoError(t, err1[0])
		require.NoError(t, err1[1])
		require.Equal(t, "user U5", users1[0].Name)
		require.Equal(t, "user U6", users1[1].Name)

		users2, err2 := thunk2()
		require.Len(t, fetches, 5) // already cached

		require.NoError(t, err2[0])
		require.Error(t, err2[1])
		require.Equal(t, "user U6", users2[0].Name)
	})

	t.Run("single error return value works", func(t *testing.T) {
		user, err := dl.Load(ctx, "F1")
		require.Error(t, err)
		require.Equal(t, "failed all fetches", err.Error())
		require.Empty(t, user)
		require.Len(t, fetches, 6)
	})

	t.Run("LoadAll does a single fetch", func(t *testing.T) {
		dl.Clear("U1")
		dl.Clear("F1")
		users, errs := dl.LoadAll(ctx, []string{"F1", "U1"})
		require.Len(t, fetches, 7)
		for _, user := range users {
			require.Empty(t, user)
		}
		require.Len(t, errs, 2)
		require.Error(t, errs[0])
		require.Equal(t, "failed all fetches", errs[0].Error())
		require.Error(t, errs[1])
		require.Equal(t, "failed all fetches", errs[1].Error())
	})
}
