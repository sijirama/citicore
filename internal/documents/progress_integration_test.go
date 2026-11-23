package documents

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestTemporaryProgress(t *testing.T) {
	url := os.Getenv("CITIZEN_TEST_REDIS_URL")
	if url == "" {
		t.Skip("set CITIZEN_TEST_REDIS_URL for Redis integration test")
	}
	progress, err := NewProgress(url)
	if err != nil {
		t.Fatal(err)
	}
	defer progress.Close()
	id := uuid.New()
	ctx := context.Background()
	defer progress.Clear(ctx, id)
	if err := progress.Start(ctx, id, "extract"); err != nil {
		t.Fatal(err)
	}
	if err := progress.Finish(ctx, id, "extract", errors.New("test failure")); err != nil {
		t.Fatal(err)
	}
	steps, err := progress.Steps(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 4 || steps[0].Status != "failed" || steps[0].Attempts != 1 {
		t.Fatalf("unexpected progress: %+v", steps)
	}
	if err := progress.ResetFrom(ctx, id, "extract"); err != nil {
		t.Fatal(err)
	}
	steps, err = progress.Steps(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("progress not reset: %+v", steps)
	}
	if err := progress.Start(ctx, id, "embed"); err != nil {
		t.Fatal(err)
	}
	if err := progress.Clear(ctx, id); err != nil {
		t.Fatal(err)
	}
	steps, err = progress.Steps(ctx, id)
	if err != nil || len(steps) != 0 {
		t.Fatalf("completed progress remains: %+v %v", steps, err)
	}
}
