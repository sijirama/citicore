// package chat validates public questions, runs the document-grounded agent, and returns only citations from retrieved passages.
package chat

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"

	"citizen/core/internal/agents"
	"citizen/core/internal/documents"
	"citizen/core/internal/runtime"
)

// Handler runs a public chat turn using the supplied configured agent.
func Handler(agent agents.Agent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Message string `json:"message"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "Send a valid question.")
			return
		}
		input.Message = strings.TrimSpace(input.Message)
		if input.Message == "" || len(input.Message) > 4000 {
			writeError(w, http.StatusBadRequest, "Your question must be between 1 and 4,000 characters.")
			return
		}
		transcript, err := runtime.Run(r.Context(), agent, input.Message)
		if err != nil {
			log.Printf("chat run failed: %v", err)
			writeError(w, http.StatusServiceUnavailable, "Citizen chat is not available right now.")
			return
		}
		var answer struct {
			Answer         string            `json:"answer"`
			CitationIDs    []string          `json:"citation_ids"`
			CitationQuotes map[string]string `json:"citation_quotes"`
		}
		if err := json.Unmarshal([]byte(transcript.FinalMessage.Content), &answer); err != nil {
			writeError(w, http.StatusBadGateway, "Citizen could not format a source-backed answer.")
			return
		}
		available := retrievedPassages(transcript.ToolResults)
		if len(available) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"answer": "I couldn't find enough support for that in Citizen's published documents yet.", "citations": []documents.SearchResult{}})
			return
		}
		citations := make([]documents.SearchResult, 0, len(answer.CitationIDs))
		seen := make(map[string]struct{}, len(answer.CitationIDs))
		for _, id := range answer.CitationIDs {
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			if passage, exists := available[id]; exists {
				if quote := answer.CitationQuotes[id]; validQuote(passage.Excerpt, quote) {
					passage.Quote = strings.TrimSpace(quote)
				}
				citations = append(citations, passage)
				seen[id] = struct{}{}
			}
		}
		if answer.Answer == "" {
			writeError(w, http.StatusBadGateway, "Citizen returned an empty answer.")
			return
		}
		if len(citations) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"answer": "I couldn't verify an answer from the passages I found. Please check the source documents directly.", "citations": []documents.SearchResult{}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"answer": answer.Answer, "citations": citations})
	}
}

var quoteWords = regexp.MustCompile(`[\p{L}\p{N}]+`)

// validQuote accepts a contiguous phrase from the retrieved passage only.
func validQuote(passage, quote string) bool {
	needle := quoteWords.FindAllString(strings.ToLower(quote), -1)
	if len(needle) < 3 || len(needle) > 40 {
		return false
	}
	haystack := quoteWords.FindAllString(strings.ToLower(passage), -1)
	for start := 0; start+len(needle) <= len(haystack); start++ {
		match := true
		for i, word := range needle {
			if haystack[start+i] != word {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// retrievedPassages extracts only document passages actually returned by the search tool.
func retrievedPassages(results []runtime.ToolResult) map[string]documents.SearchResult {
	passages := make(map[string]documents.SearchResult)
	for _, result := range results {
		if result.Name != "search_documents" {
			continue
		}
		var found []documents.SearchResult
		if json.Unmarshal(result.Output, &found) != nil {
			continue
		}
		for _, passage := range found {
			if passage.ID != "" {
				passages[passage.ID] = passage
			}
		}
	}
	return passages
}

// writeError sends a client-safe chat error without leaking provider or storage details.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeJSON writes one API response as JSON.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
