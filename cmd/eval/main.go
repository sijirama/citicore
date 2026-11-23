// Run a small, inspectable chat evaluation against a local document database.
package main

import (
	"citizen/core/internal/agents"
	"citizen/core/internal/documents"
	"citizen/core/internal/llm"
	"citizen/core/internal/runtime"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	redisclient "github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var questions = []string{
	"What are my fundamental rights under the Nigerian Constitution?",
	"Can the police arrest me without a warrant?",
	"What does each level of government do?",
	"Who can make laws on defence and aviation?",
	"What are local government councils responsible for?",
	"How is the President elected?",
	"Can a Nigerian citizen be denied freedom of movement?",
	"Who appoints the Chief Justice of Nigeria?",
	"Where is my polling unit for the 2027 election?",
	"What are today's election results?",
}

func main() {
	_ = godotenv.Load()
	if len(os.Args) > 1 {
		questions = os.Args[1:]
	}
	db, err := gorm.Open(postgres.Open(os.Getenv("DATABASE_URL")), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	client, err := llm.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	redisOptions, err := redisclient.ParseURL(env("REDIS_URL", "redis://localhost:6382"))
	if err != nil {
		log.Fatal(err)
	}
	embeddingCache := redisclient.NewClient(redisOptions)
	defer embeddingCache.Close()
	searcher := &documents.HybridSearcher{DB: db, Embedder: documents.NewEmbedder(os.Getenv("OPENAI_API_KEY"), os.Getenv("EMBEDDING_MODEL")), EmbeddingCache: embeddingCache}
	encoder := json.NewEncoder(os.Stdout)
	for _, question := range questions {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		transcript, runErr := runtime.Run(ctx, agents.NewChat(client, searcher), question)
		cancel()
		item := map[string]any{"question": question}
		if runErr != nil {
			item["error"] = runErr.Error()
			_ = encoder.Encode(item)
			continue
		}
		item["answer"] = transcript.FinalMessage.Content
		searches := make([]map[string]any, 0, len(transcript.ToolResults))
		for _, result := range transcript.ToolResults {
			var args map[string]any
			var passages []documents.SearchResult
			_ = json.Unmarshal(result.Arguments, &args)
			_ = json.Unmarshal(result.Output, &passages)
			matches := make([]map[string]any, 0, len(passages))
			for _, passage := range passages {
				excerpt := passage.Excerpt
				if len(excerpt) > 220 {
					excerpt = excerpt[:220]
				}
				matches = append(matches, map[string]any{"id": passage.ID, "page": passage.Page, "section": passage.Section, "excerpt": excerpt})
			}
			searches = append(searches, map[string]any{"tool": result.Name, "arguments": args, "passages": matches})
		}
		item["searches"] = searches
		if err := encoder.Encode(item); err != nil {
			log.Fatal(fmt.Errorf("write evaluation: %w", err))
		}
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
