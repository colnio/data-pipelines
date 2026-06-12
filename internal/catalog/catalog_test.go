package catalog_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/colnio/data-pipelines/internal/catalog"
	"github.com/colnio/data-pipelines/internal/testsupport"
)

// TestListSamples verifies that a seeded sample appears in ListSamples
// and can be retrieved by GetSample.
func TestListAndGetSamples(t *testing.T) {
	pool := testsupport.NewPool(t)
	svc := catalog.NewService(pool)
	t.Cleanup(func() {
		testsupport.Truncate(t, pool, "contact_configs", "devices", "samples")
	})

	ctx := context.Background()

	// Insert a sample directly into the base table (views are read-only).
	_, err := pool.Exec(ctx, `
		INSERT INTO samples (id, material_stack, notes)
		VALUES ('S001', 'graphene/SiO2', 'test sample')`)
	require.NoError(t, err)

	// List — should return the inserted sample.
	samples, next, err := svc.ListSamples(ctx, 10, "")
	require.NoError(t, err)
	assert.Empty(t, next, "no next cursor expected for single item < limit")
	require.Len(t, samples, 1)
	assert.Equal(t, "S001", samples[0].ID)
	assert.NotNil(t, samples[0].MaterialStack)
	assert.Equal(t, "graphene/SiO2", *samples[0].MaterialStack)

	// Get — by id.
	s, err := svc.GetSample(ctx, "S001")
	require.NoError(t, err)
	assert.Equal(t, "S001", s.ID)

	// Get — not found.
	_, err = svc.GetSample(ctx, "NOTEXIST")
	require.Error(t, err)
}

// TestListDevices verifies device listing with sample_id filter.
func TestListAndGetDevices(t *testing.T) {
	pool := testsupport.NewPool(t)
	svc := catalog.NewService(pool)
	t.Cleanup(func() {
		testsupport.Truncate(t, pool, "contact_configs", "devices", "samples")
	})

	ctx := context.Background()

	// Seed a sample, then two devices (one linked, one unlinked).
	_, err := pool.Exec(ctx, `INSERT INTO samples (id) VALUES ('S002')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO devices (id, sample_id, device_class)
		VALUES ('D001', 'S002', 'fet'), ('D002', NULL, 'capacitor')`)
	require.NoError(t, err)

	// List all.
	devs, _, err := svc.ListDevices(ctx, "", 10, "")
	require.NoError(t, err)
	assert.Len(t, devs, 2)

	// List filtered by sample_id.
	devs, _, err = svc.ListDevices(ctx, "S002", 10, "")
	require.NoError(t, err)
	require.Len(t, devs, 1)
	assert.Equal(t, "D001", devs[0].ID)

	// Get.
	d, err := svc.GetDevice(ctx, "D001")
	require.NoError(t, err)
	assert.Equal(t, "fet", d.DeviceClass)

	// Get — not found.
	_, err = svc.GetDevice(ctx, "NOTEXIST")
	require.Error(t, err)
}

// TestListContactConfigs verifies contact_config listing with device_id filter.
func TestListContactConfigs(t *testing.T) {
	pool := testsupport.NewPool(t)
	svc := catalog.NewService(pool)
	t.Cleanup(func() {
		testsupport.Truncate(t, pool, "contact_configs", "devices", "samples")
	})

	ctx := context.Background()

	// Seed sample → device → two contact_configs.
	_, err := pool.Exec(ctx, `INSERT INTO samples (id) VALUES ('S003')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO devices (id, sample_id, device_class) VALUES ('D003', 'S003', 'fet')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO contact_configs (id, device_id, is_default)
		VALUES ('CC001', 'D003', true), ('CC002', 'D003', false)`)
	require.NoError(t, err)

	// List filtered by device_id.
	cfgs, next, err := svc.ListContactConfigs(ctx, "D003", 10, "")
	require.NoError(t, err)
	assert.Empty(t, next)
	assert.Len(t, cfgs, 2)

	// List without filter.
	cfgs, _, err = svc.ListContactConfigs(ctx, "", 10, "")
	require.NoError(t, err)
	assert.Len(t, cfgs, 2)
}

// TestCursorPagination verifies that the keyset cursor pages correctly.
func TestCursorPagination(t *testing.T) {
	pool := testsupport.NewPool(t)
	svc := catalog.NewService(pool)
	t.Cleanup(func() {
		testsupport.Truncate(t, pool, "contact_configs", "devices", "samples")
	})

	ctx := context.Background()

	// Insert 3 samples.
	for _, id := range []string{"SA", "SB", "SC"} {
		_, err := pool.Exec(ctx, `INSERT INTO samples (id) VALUES ($1)`, id)
		require.NoError(t, err)
	}

	// Fetch page 1 (limit=2).
	page1, next, err := svc.ListSamples(ctx, 2, "")
	require.NoError(t, err)
	assert.Len(t, page1, 2)
	assert.NotEmpty(t, next, "next cursor should be set when full page returned")

	// Fetch page 2 using cursor.
	page2, next2, err := svc.ListSamples(ctx, 2, next)
	require.NoError(t, err)
	assert.Len(t, page2, 1)
	assert.Empty(t, next2, "no more pages")

	// Ensure no overlap.
	ids1 := map[string]bool{page1[0].ID: true, page1[1].ID: true}
	assert.False(t, ids1[page2[0].ID], "page 2 item should not appear in page 1")
}
