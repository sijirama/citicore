package documents

import "gorm.io/gorm"

// AutoMigrate creates and updates the document tables from their GORM models.
func AutoMigrate(db *gorm.DB) error {
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return err
	}
	if err := db.AutoMigrate(&Document{}, &Chunk{}); err != nil {
		return err
	}
	return db.Exec(`CREATE INDEX IF NOT EXISTS document_chunks_embedding_hnsw_idx ON document_chunks USING hnsw (embedding vector_cosine_ops) WHERE embedding IS NOT NULL`).Error
}
