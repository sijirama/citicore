package documents

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var Steps = []string{"extract", "classify", "chunk", "embed"}

type Document struct {
	ID            uuid.UUID  `json:"id" gorm:"type:uuid;primaryKey"`
	Title         string     `json:"title" gorm:"type:text;not null"`
	SourceURL     string     `json:"sourceUrl" gorm:"type:text;not null;default:''"`
	VersionLabel  string     `json:"versionLabel" gorm:"type:text;not null;default:''"`
	Jurisdiction  string     `json:"jurisdiction" gorm:"type:text;not null;default:''"`
	DocumentType  string     `json:"documentType" gorm:"type:text;not null;default:''"`
	Summary       string     `json:"summary" gorm:"type:text;not null;default:''"`
	ObjectKey     string     `json:"-" gorm:"type:text;not null;unique"`
	FileName      string     `json:"fileName" gorm:"type:text;not null"`
	ContentType   string     `json:"contentType" gorm:"type:text;not null"`
	SHA256        string     `json:"sha256" gorm:"column:sha256;type:text;not null"`
	Bytes         int64      `json:"bytes" gorm:"not null"`
	ExtractedText string     `json:"-" gorm:"type:text;not null;default:''"`
	Status        string     `json:"status" gorm:"type:text;not null;default:processing;index:documents_status_idx,priority:1;check:documents_status_check,status IN ('processing','failed','review','published','archived')"`
	LastError     string     `json:"lastError" gorm:"type:text;not null;default:''"`
	PublishedAt   *time.Time `json:"publishedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt" gorm:"not null;default:CURRENT_TIMESTAMP;index:documents_status_idx,priority:2,sort:desc"`
	UpdatedAt     time.Time  `json:"updatedAt" gorm:"not null;default:CURRENT_TIMESTAMP"`
	Chunks        []Chunk    `json:"-" gorm:"foreignKey:DocumentID;constraint:document_chunks_document_id_fkey,OnDelete:CASCADE"`
	Steps         []Step     `json:"steps,omitempty" gorm:"-"`
	ChunkCount    int        `json:"chunkCount" gorm:"-"`
	EmbeddedCount int        `json:"embeddedCount" gorm:"-"`
}

type Step struct {
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Attempts   int        `json:"attempts"`
	Error      string     `json:"error"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type Chunk struct {
	ID           uuid.UUID `json:"id" gorm:"type:uuid;primaryKey"`
	DocumentID   uuid.UUID `json:"documentId" gorm:"type:uuid;not null;uniqueIndex:document_chunks_document_id_ordinal_key;index:document_chunks_document_idx,priority:1"`
	Ordinal      int       `json:"ordinal" gorm:"type:integer;not null;uniqueIndex:document_chunks_document_id_ordinal_key;index:document_chunks_document_idx,priority:2"`
	Section      string    `json:"section" gorm:"type:text;not null;default:''"`
	Page         int       `json:"page" gorm:"type:integer;not null;default:0"`
	StartLine    int       `json:"startLine" gorm:"type:integer;not null;default:0"`
	EndLine      int       `json:"endLine" gorm:"type:integer;not null;default:0"`
	PageWidth    float32   `json:"pageWidth" gorm:"type:real;not null;default:0"`
	PageHeight   float32   `json:"pageHeight" gorm:"type:real;not null;default:0"`
	PDFWords     []PDFWord `json:"-" gorm:"type:jsonb;serializer:json"`
	Content      string    `json:"content" gorm:"type:text;not null"`
	SearchText   string    `json:"searchText" gorm:"type:text;not null"`
	Embedding    *string   `json:"-" gorm:"type:vector(1536);->"`
	SearchVector string    `json:"-" gorm:"type:tsvector GENERATED ALWAYS AS (to_tsvector('english', search_text)) STORED;->;index:document_chunks_search_idx,type:gin"`
}

func (Chunk) TableName() string { return "document_chunks" }

type Store struct{ DB *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{DB: db} }

func (s *Store) Create(ctx context.Context, doc Document) error {
	return s.DB.WithContext(ctx).Create(&doc).Error
}
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Document, error) {
	var d Document
	err := s.DB.WithContext(ctx).First(&d, "id = ?", id).Error
	if err != nil {
		return d, err
	}
	var count int64
	err = s.DB.WithContext(ctx).Model(&Chunk{}).Where("document_id = ?", id).Count(&count).Error
	d.ChunkCount = int(count)
	if err == nil {
		err = s.DB.WithContext(ctx).Model(&Chunk{}).Where("document_id = ? AND embedding IS NOT NULL", id).Count(&count).Error
		d.EmbeddedCount = int(count)
	}
	return d, err
}
func (s *Store) List(ctx context.Context) ([]Document, error) {
	result := make([]Document, 0)
	err := s.DB.WithContext(ctx).Order("created_at DESC").Limit(100).Find(&result).Error
	return result, err
}
func (s *Store) FinishStep(ctx context.Context, id uuid.UUID, step string, processErr error) error {
	message, status := "", "processing"
	if processErr != nil {
		message = processErr.Error()
		if len(message) > 1000 {
			message = message[:1000]
		}
		status = "failed"
	} else if step == "embed" {
		status = "review"
	}
	return s.DB.WithContext(ctx).Model(&Document{}).Where("id = ?", id).Updates(map[string]any{"status": status, "last_error": message}).Error
}
func (s *Store) SaveExtraction(ctx context.Context, id uuid.UUID, content string) error {
	return s.DB.WithContext(ctx).Model(&Document{}).Where("id = ?", id).Update("extracted_text", content).Error
}
func (s *Store) ExtractedText(ctx context.Context, id uuid.UUID) (string, error) {
	var d Document
	err := s.DB.WithContext(ctx).Select("extracted_text").First(&d, "id = ?", id).Error
	return d.ExtractedText, err
}
func (s *Store) SaveClassification(ctx context.Context, id uuid.UUID, kind, jurisdiction, summary string) error {
	return s.DB.WithContext(ctx).Model(&Document{}).Where("id = ?", id).Updates(map[string]any{"document_type": kind, "jurisdiction": jurisdiction, "summary": summary}).Error
}
func (s *Store) ReplaceChunks(ctx context.Context, id uuid.UUID, chunks []Chunk) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("document_id = ?", id).Delete(&Chunk{}).Error; err != nil {
			return err
		}
		if len(chunks) > 0 {
			return tx.CreateInBatches(&chunks, 100).Error
		}
		return nil
	})
}
func (s *Store) Chunks(ctx context.Context, id uuid.UUID) ([]Chunk, error) {
	chunks := make([]Chunk, 0)
	err := s.DB.WithContext(ctx).Select("id", "search_text").Where("document_id = ?", id).Order("ordinal").Find(&chunks).Error
	return chunks, err
}
func VectorLiteral(values []float32) (string, error) {
	if len(values) != 1536 {
		return "", fmt.Errorf("embedding has %d dimensions, expected 1536", len(values))
	}
	parts := make([]string, len(values))
	for i, v := range values {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return "", fmt.Errorf("nonfinite embedding value")
		}
		parts[i] = fmt.Sprintf("%g", v)
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}
func (s *Store) SaveEmbedding(ctx context.Context, id uuid.UUID, values []float32) error {
	literal, err := VectorLiteral(values)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Exec("UPDATE document_chunks SET embedding = ?::vector WHERE id = ?", literal, id).Error
}
func (s *Store) Retry(ctx context.Context, id uuid.UUID, from string) error {
	position := -1
	for i, name := range Steps {
		if name == from {
			position = i
		}
	}
	if position < 0 {
		return fmt.Errorf("unknown step %q", from)
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var d Document
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&d, "id = ?", id).Error; err != nil {
			return err
		}
		if d.Status == "processing" {
			return fmt.Errorf("document is still processing")
		}
		if position <= 2 {
			if err := tx.Where("document_id = ?", id).Delete(&Chunk{}).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Exec("UPDATE document_chunks SET embedding = NULL WHERE document_id = ?", id).Error; err != nil {
				return err
			}
		}
		return tx.Model(&Document{}).Where("id = ?", id).Updates(map[string]any{"status": "processing", "last_error": "", "published_at": nil}).Error
	})
}
func (s *Store) Publish(ctx context.Context, id uuid.UUID) error {
	tx := s.DB.WithContext(ctx).Exec("UPDATE documents SET status = 'published', published_at = now(), updated_at = now() WHERE id = ? AND status = 'review' AND EXISTS (SELECT 1 FROM document_chunks WHERE document_id = ? AND embedding IS NOT NULL)", id, id)
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return fmt.Errorf("document is not ready for publication")
	}
	return nil
}
func (s *Store) Archive(ctx context.Context, id uuid.UUID) error {
	tx := s.DB.WithContext(ctx).Model(&Document{}).Where("id = ? AND status = 'published'", id).Update("status", "archived")
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return fmt.Errorf("document is not published")
	}
	return nil
}

// ChunkInspect includes only the information an admin needs to inspect retrieval material.
type ChunkInspect struct {
	ID         uuid.UUID `json:"id"`
	Ordinal    int       `json:"ordinal"`
	Section    string    `json:"section"`
	Page       int       `json:"page"`
	StartLine  int       `json:"startLine"`
	EndLine    int       `json:"endLine"`
	Positioned bool      `json:"positioned"`
	Content    string    `json:"content"`
	Embedded   bool      `json:"embedded"`
}

func (s *Store) InspectChunks(ctx context.Context, id uuid.UUID, offset, limit int) ([]ChunkInspect, error) {
	items := make([]ChunkInspect, 0)
	err := s.DB.WithContext(ctx).Raw("SELECT id, ordinal, section, page, start_line, end_line, CASE WHEN jsonb_typeof(pdf_words) = 'array' THEN jsonb_array_length(pdf_words) > 0 ELSE FALSE END AS positioned, content, (embedding IS NOT NULL) AS embedded FROM document_chunks WHERE document_id = ? ORDER BY ordinal LIMIT ? OFFSET ?", id, limit, offset).Scan(&items).Error
	return items, err
}

// PublishedPassage returns the PDF coordinates only for a publicly available source.
func (s *Store) PublishedPassage(ctx context.Context, id uuid.UUID) (Chunk, error) {
	var passage Chunk
	err := s.DB.WithContext(ctx).Model(&Chunk{}).
		Joins("JOIN documents ON documents.id = document_chunks.document_id").
		Where("document_chunks.id = ? AND documents.status = ?", id, "published").
		Select("document_chunks.*").First(&passage).Error
	return passage, err
}
