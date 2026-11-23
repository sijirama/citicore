package documents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"gorm.io/gorm"
)

type HybridSearcher struct {
	DB             *gorm.DB
	Embedder       *Embedder
	EmbeddingCache *redis.Client
}

type candidate struct {
	Result SearchResult
	Score  float64
}

func (s *HybridSearcher) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	started := time.Now()
	if limit < 1 || limit > 12 {
		limit = 6
	}
	candidates := make(map[string]*candidate)
	add := func(r SearchResult, rank int) {
		item := candidates[r.ID]
		if item == nil {
			item = &candidate{Result: r}
			candidates[r.ID] = item
		}
		item.Score += 1 / float64(60+rank)
	}
	var lexical []SearchResult
	var lexicalErr error
	var lexicalDuration time.Duration
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		lexicalStarted := time.Now()
		lexical, lexicalErr = s.lexical(ctx, query)
		lexicalDuration = time.Since(lexicalStarted)
	}()
	var vectors [][]float32
	var embeddingErr error
	embeddingStarted := time.Now()
	if s.Embedder != nil {
		vectors, embeddingErr = s.embedQuery(ctx, query)
	}
	embeddingDuration := time.Since(embeddingStarted)
	wg.Wait()
	for i, r := range lexical {
		add(r, i+1)
	}
	semanticErr := embeddingErr
	semanticDuration := time.Duration(0)
	if s.Embedder != nil && embeddingErr == nil {
		semanticStarted := time.Now()
		semantic, err := s.semantic(ctx, vectors[0])
		semanticDuration = time.Since(semanticStarted)
		semanticErr = err
		if err == nil {
			for i, r := range semantic {
				add(r, i+1)
			}
		}
		log.Printf("retrieval timings lexical=%s embedding=%s vector=%s candidates=%d", lexicalDuration.Round(time.Millisecond), embeddingDuration.Round(time.Millisecond), semanticDuration.Round(time.Millisecond), len(candidates))
	} else if s.Embedder != nil {
		log.Printf("retrieval timings lexical=%s embedding=%s vector=skipped candidates=%d embedding_error=%v", lexicalDuration.Round(time.Millisecond), embeddingDuration.Round(time.Millisecond), len(candidates), embeddingErr)
	} else {
		log.Printf("retrieval timings lexical=%s embedding=disabled candidates=%d", lexicalDuration.Round(time.Millisecond), len(candidates))
	}
	if len(candidates) == 0 && lexicalErr != nil {
		return nil, lexicalErr
	}
	if len(candidates) == 0 && semanticErr != nil {
		return nil, semanticErr
	}
	ranked := make([]*candidate, 0, len(candidates))
	for _, item := range candidates {
		ranked = append(ranked, item)
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	if len(ranked) > 16 {
		ranked = ranked[:16]
	}
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	log.Printf("retrieval complete total=%s returned=%d", time.Since(started).Round(time.Millisecond), len(ranked))
	results := make([]SearchResult, len(ranked))
	for i, item := range ranked {
		results[i] = item.Result
	}
	return results, nil
}

func (s *HybridSearcher) embedQuery(ctx context.Context, query string) ([][]float32, error) {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	hash := sha256.Sum256([]byte(normalized))
	key := "citizen:embedding:v1:" + hex.EncodeToString(hash[:])
	if s.EmbeddingCache != nil {
		if cached, err := s.EmbeddingCache.Get(ctx, key).Result(); err == nil {
			var v []float32
			if json.Unmarshal([]byte(cached), &v) == nil && len(v) == 1536 {
				log.Printf("retrieval embedding_cache=hit")
				return [][]float32{v}, nil
			}
		}
	}
	vectors, err := s.Embedder.Embed(ctx, []string{query})
	if err != nil || s.EmbeddingCache == nil || len(vectors) == 0 {
		return vectors, err
	}
	if encoded, e := json.Marshal(vectors[0]); e == nil {
		_ = s.EmbeddingCache.Set(ctx, key, encoded, 24*time.Hour).Err()
	}
	return vectors, nil
}

const candidateColumns = `c.id::text,d.title,d.version_label,c.section,c.page,c.content,d.source_url,d.id::text,d.content_type`

func readCandidates(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]SearchResult, error) {
	defer rows.Close()
	results := make([]SearchResult, 0)
	for rows.Next() {
		var item SearchResult
		var documentID string
		if err := rows.Scan(&item.ID, &item.Title, &item.Version, &item.Section, &item.Page, &item.Excerpt, &item.SourceURL, &documentID, &item.ContentType); err != nil {
			return nil, err
		}
		item.Document = item.Title
		item.SourceURL = "/api/documents/" + documentID + "/source"
		results = append(results, item)
	}
	return results, rows.Err()
}

func (s *HybridSearcher) lexical(ctx context.Context, query string) ([]SearchResult, error) {
	rows, err := s.DB.WithContext(ctx).Raw(`SELECT `+candidateColumns+` FROM document_chunks c JOIN documents d ON d.id=c.document_id,
        websearch_to_tsquery('english',?) q
        WHERE d.status='published' AND c.search_vector @@ q
        ORDER BY ts_rank_cd(c.search_vector,q) DESC LIMIT 25`, query).Rows()
	if err != nil {
		return nil, err
	}
	return readCandidates(rows)
}

func (s *HybridSearcher) semantic(ctx context.Context, vector []float32) ([]SearchResult, error) {
	literal, err := VectorLiteral(vector)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.WithContext(ctx).Raw(`SELECT `+candidateColumns+` FROM document_chunks c JOIN documents d ON d.id=c.document_id
        WHERE d.status='published' AND c.embedding IS NOT NULL
        ORDER BY c.embedding <=> ?::vector LIMIT 25`, literal).Rows()
	if err != nil {
		return nil, err
	}
	return readCandidates(rows)
}
