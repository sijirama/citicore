// package documents defines the bounded search contract shared by chat retrieval and document administration.
package documents

import "context"

// SearchResult is a source passage that can be shown beside an answer.
type SearchResult struct {
	ID          string `json:"id"`
	Document    string `json:"document"`
	Title       string `json:"title"`
	Version     string `json:"version,omitempty"`
	Section     string `json:"section,omitempty"`
	Page        int    `json:"page,omitempty"`
	Excerpt     string `json:"excerpt"`
	Quote       string `json:"quote,omitempty"`
	SourceURL   string `json:"sourceUrl,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

// Searcher searches only documents approved for the Citizen public library.
type Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)
}
