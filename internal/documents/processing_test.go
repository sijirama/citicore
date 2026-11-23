package documents

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestExtractAndChunkText(t *testing.T) {
	input := "Chapter 1\nThe Nigerian Constitution establishes the civic framework for the federation.\fSection 2\nPublic institutions have responsibilities under the law."
	extracted, err := Extract(context.Background(), "text/plain", []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	chunks := ChunkByStructure(uuid.New(), "Constitution", "1999", extracted)
	if len(chunks) != 2 {
		t.Fatalf("want 2 page passages, got %d", len(chunks))
	}
	if chunks[0].Page != 1 || chunks[1].Page != 2 {
		t.Fatalf("page metadata: %d, %d", chunks[0].Page, chunks[1].Page)
	}
	if !strings.Contains(chunks[0].SearchText, "Constitution (1999)") {
		t.Fatalf("missing title/version context: %q", chunks[0].SearchText)
	}
}
func TestVectorLiteralRejectsInvalidValues(t *testing.T) {
	if _, err := VectorLiteral(make([]float32, 3)); err == nil {
		t.Fatal("wrong dimension accepted")
	}
}
