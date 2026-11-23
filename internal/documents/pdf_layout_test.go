package documents

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestParsePDFLayoutKeepsPageLineAndWordBoxes(t *testing.T) {
	const source = `<html><body><doc>
<page width="200" height="300"><flow><block><line>
<word xMin="10" yMin="20" xMax="40" yMax="32">Right</word>
<word xMin="44" yMin="20" xMax="70" yMax="32">to</word>
<word xMin="74" yMin="20" xMax="100" yMax="32">life</word>
</line></block></flow></page>
<page width="200" height="300"><flow><block><line>
<word xMin="12" yMin="15" xMax="50" yMax="28">Liberty</word>
</line></block></flow></page></doc></body></html>`
	pages, err := parsePDFLayout(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].Width != 200 || pages[1].Blocks[0].Lines[0].Number != 1 {
		t.Fatalf("incorrect page/line parsing: %+v", pages)
	}
	line := pages[0].Blocks[0].Lines[0]
	if line.Text != "Right to life" || len(line.Words) != 3 || line.Words[0].Box.Y0 != 20 || line.Words[2].Box.X1 != 100 {
		t.Fatalf("word text or coordinates were lost: %+v", line)
	}
}

// Run with CITIZEN_PDF_FIXTURE to check Poppler output against a real local PDF.
func TestChunkRealPDFPositions(t *testing.T) {
	path := os.Getenv("CITIZEN_PDF_FIXTURE")
	if path == "" {
		t.Skip("set CITIZEN_PDF_FIXTURE for real PDF position test")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	chunks, err := ChunkPDF(context.Background(), id, "Constitution", "1999", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 10 {
		t.Fatalf("too few positioned passages: %d", len(chunks))
	}
	for _, chunk := range chunks {
		if chunk.Page < 1 || chunk.PageWidth <= 0 || chunk.PageHeight <= 0 || chunk.StartLine < 1 || chunk.EndLine < chunk.StartLine || len(chunk.PDFWords) == 0 {
			t.Fatalf("passage %d has incomplete position metadata: page=%d lines=%d-%d words=%d", chunk.Ordinal, chunk.Page, chunk.StartLine, chunk.EndLine, len(chunk.PDFWords))
		}
		for _, word := range chunk.PDFWords {
			if word.Line < chunk.StartLine || word.Line > chunk.EndLine || word.Box.X0 < 0 || word.Box.Y0 < 0 || word.Box.X1 > chunk.PageWidth+1 || word.Box.Y1 > chunk.PageHeight+1 {
				t.Fatalf("passage %d has an out-of-range word: %+v", chunk.Ordinal, word)
			}
		}
	}
	t.Logf("positioned %d passages across %d PDF pages", len(chunks), chunks[len(chunks)-1].Page)
	if dsn := os.Getenv("CITIZEN_TEST_DATABASE_URL"); dsn != "" {
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if err := AutoMigrate(db); err != nil {
			t.Fatal(err)
		}
		store := NewStore(db)
		doc := Document{ID: id, Title: "Position test", ObjectKey: "test/" + id.String(), FileName: "source.pdf", ContentType: "application/pdf", SHA256: "test", Bytes: int64(len(data)), Status: "processing"}
		if err := store.Create(context.Background(), doc); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Exec("DELETE FROM documents WHERE id = ?", id) })
		if err := store.ReplaceChunks(context.Background(), id, chunks); err != nil {
			t.Fatal(err)
		}
		var stored Chunk
		if err := db.First(&stored, "id = ?", chunks[len(chunks)/2].ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.PageWidth <= 0 || len(stored.PDFWords) == 0 || stored.StartLine == 0 {
			t.Fatalf("position data was not saved with the passage: %+v", stored)
		}
	}
}
