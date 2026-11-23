package documents

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "sort"
    "strings"
    "time"
)

type Embedder struct {
    APIKey string
    Model string
    Client *http.Client
}

func NewEmbedder(key, model string) *Embedder {
    if model == "" { model = "text-embedding-3-small" }
    return &Embedder{APIKey: key, Model: model, Client: &http.Client{Timeout: 60 * time.Second}}
}

func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
    if e.APIKey == "" { return nil, fmt.Errorf("OPENAI_API_KEY is required for embeddings") }
    if len(texts) == 0 { return nil, fmt.Errorf("no text to embed") }
    payload, err := json.Marshal(map[string]any{"model": e.Model, "input": texts, "dimensions": 1536})
    if err != nil { return nil, err }
    request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/embeddings", bytes.NewReader(payload))
    if err != nil { return nil, err }
    request.Header.Set("Authorization", "Bearer "+e.APIKey)
    request.Header.Set("Content-Type", "application/json")
    response, err := e.Client.Do(request)
    if err != nil { return nil, err }
    defer response.Body.Close()
    data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
    if err != nil { return nil, err }
    if response.StatusCode < 200 || response.StatusCode >= 300 {
        return nil, fmt.Errorf("embedding endpoint returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
    }
    var result struct { Data []struct { Index int `json:"index"`; Embedding []float32 `json:"embedding"` } `json:"data"` }
    if err := json.Unmarshal(data, &result); err != nil { return nil, err }
    if len(result.Data) != len(texts) { return nil, fmt.Errorf("embedding endpoint returned %d vectors for %d texts", len(result.Data), len(texts)) }
    sort.Slice(result.Data, func(i,j int) bool { return result.Data[i].Index < result.Data[j].Index })
    vectors := make([][]float32, len(texts))
    for i, item := range result.Data {
        if item.Index != i || len(item.Embedding) != 1536 { return nil, fmt.Errorf("embedding %d has unexpected index or dimensions", i) }
        vectors[i] = item.Embedding
    }
    return vectors, nil
}
