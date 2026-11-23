package documents

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDocumentLifecycle(t *testing.T) {
	dsn := os.Getenv("CITIZEN_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set CITIZEN_TEST_DATABASE_URL for Postgres integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := NewStore(db)
	id := uuid.New()
	t.Cleanup(func() { db.Exec("DELETE FROM documents WHERE id = ?", id) })
	doc := Document{ID: id, Title: "Test Constitution", ObjectKey: "test/" + id.String(), FileName: "test.txt", ContentType: "text/plain", SHA256: "test", Bytes: 40, Status: "processing"}
	if err := store.Create(ctx, doc); err != nil {
		t.Fatal(err)
	}

	if err := store.SaveExtraction(ctx, id, "This document describes a test civic right."); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishStep(ctx, id, "extract", nil); err != nil {
		t.Fatal(err)
	}
	text, err := store.ExtractedText(ctx, id)
	if err != nil || text == "" {
		t.Fatalf("extracted text %q: %v", text, err)
	}
	chunk := Chunk{ID: uuid.New(), DocumentID: id, Ordinal: 0, Page: 1, StartLine: 2, EndLine: 2, PageWidth: 200, PageHeight: 300, PDFWords: []PDFWord{{Text: "This", Line: 2, Box: PDFBox{X0: 10, Y0: 20, X1: 30, Y1: 32}}}, Content: text, SearchText: "Test Constitution " + text}
	if err := store.ReplaceChunks(ctx, id, []Chunk{chunk}); err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, 1536)
	vector[0] = 1
	if err := store.SaveEmbedding(ctx, chunk.ID, vector); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"classify", "chunk", "embed"} {

		if err := store.FinishStep(ctx, id, step, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "review" || got.ChunkCount != 1 || got.EmbeddedCount != 1 {
		t.Fatalf("unexpected review state: %+v", got)
	}
	preview, err := store.InspectChunks(ctx, id, 0, 40)
	if err != nil || len(preview) != 1 || !preview[0].Embedded || !preview[0].Positioned || preview[0].StartLine != 2 {
		t.Fatalf("chunk inspection: %+v, %v", preview, err)
	}
	var stored Chunk
	if err := db.First(&stored, "id = ?", chunk.ID).Error; err != nil || len(stored.PDFWords) != 1 || stored.PDFWords[0].Box.Y0 != 20 {
		t.Fatalf("position storage: %+v, %v", stored.PDFWords, err)
	}
	if _, err := store.PublishedPassage(ctx, chunk.ID); err == nil {
		t.Fatal("unpublished passage position was public")
	}
	if err := store.Publish(ctx, id); err != nil {
		t.Fatal(err)
	}
	public, err := store.PublishedPassage(ctx, chunk.ID)
	if err != nil || len(public.PDFWords) != 1 || public.PDFWords[0].Text != "This" {
		t.Fatalf("published passage position: %+v, %v", public.PDFWords, err)
	}
	searcher := &HybridSearcher{DB: db}
	hits, err := searcher.Search(ctx, "civic right", 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hit := range hits {
		if hit.ID == chunk.ID.String() {
			found = true
		}
	}
	if !found {
		t.Fatalf("published passage missing from search: %+v", hits)
	}
	if err := store.Archive(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishedPassage(ctx, chunk.ID); err == nil {
		t.Fatal("archived passage position was public")
	}
	hits, err = searcher.Search(ctx, "civic right", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range hits {
		if hit.ID == chunk.ID.String() {
			t.Fatal("archived passage remains searchable")
		}
	}
	if err := store.Retry(ctx, id, "embed"); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "processing" {
		t.Fatalf("retry state: %+v", got)
	}
}
