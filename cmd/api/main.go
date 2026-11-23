// package main starts Citizen's HTTP API and wires authentication before the remaining services are added.
package main

import (
	"citizen/core/internal/admin"
	"citizen/core/internal/documents"
	"citizen/core/internal/ingest"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	redisclient "github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/joho/godotenv"

	"citizen/core/internal/agents"
	"citizen/core/internal/auth"
	"citizen/core/internal/chat"
	"citizen/core/internal/llm"
)

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("could not read .env: %v", err)
	}
	admins := strings.Split(os.Getenv("ADMIN_EMAILS"), ",")
	service := auth.New(auth.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		CallbackURL:  env("GOOGLE_CALLBACK_URL", "http://localhost:8081/auth/google/callback"),
		SessionKey:   os.Getenv("SESSION_SECRET"),
		CookieDomain: os.Getenv("SESSION_COOKIE_DOMAIN"),
		ClientOrigin: env("CLIENT_ORIGIN", "http://localhost:5173"),
		Environment:  os.Getenv("APP_ENV"),
		AdminEmails:  admins,
	})

	db, err := gorm.Open(postgres.Open(os.Getenv("DATABASE_URL")), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	if err := documents.AutoMigrate(db); err != nil {
		log.Fatal(err)
	}
	objects, err := documents.NewObjects(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	redis, err := ingest.RedisOption()
	if err != nil {
		log.Fatal(err)
	}
	queue := asynq.NewClient(redis)
	defer queue.Close()
	progress, err := documents.NewProgress(os.Getenv("REDIS_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer progress.Close()
	store := documents.NewStore(db)
	embedder := documents.NewEmbedder(os.Getenv("OPENAI_API_KEY"), os.Getenv("EMBEDDING_MODEL"))
	workflow := &ingest.Workflow{Store: store, Progress: progress, Objects: objects, Queue: queue}
	handlers := &admin.Documents{Store: store, Progress: progress, Objects: objects, Workflow: workflow}
	redisOptions, err := redisclient.ParseURL(env("REDIS_URL", "redis://localhost:6382"))
	if err != nil {
		log.Fatal(err)
	}
	embeddingCache := redisclient.NewClient(redisOptions)
	defer embeddingCache.Close()
	searcher := &documents.HybridSearcher{DB: db, Embedder: embedder, EmbeddingCache: embeddingCache}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /auth/google", service.Login)
	mux.HandleFunc("GET /auth/google/callback", service.Callback)
	mux.HandleFunc("POST /auth/logout", service.Logout)
	mux.HandleFunc("POST /api/chat", chat.Handler(agents.NewChat(modelClient(), searcher)))
	mux.Handle("GET /api/admin/me", service.RequireAdmin(http.HandlerFunc(service.Me)))
	mux.Handle("GET /api/admin/documents", service.RequireAdmin(http.HandlerFunc(handlers.List)))
	mux.Handle("POST /api/admin/documents", service.RequireAdmin(http.HandlerFunc(handlers.Upload)))
	mux.Handle("GET /api/admin/documents/{id}", service.RequireAdmin(http.HandlerFunc(handlers.Get)))
	mux.Handle("POST /api/admin/documents/{id}/{action}", service.RequireAdmin(http.HandlerFunc(handlers.Action)))
	mux.Handle("GET /api/admin/documents/{id}/chunks", service.RequireAdmin(http.HandlerFunc(handlers.Chunks)))
	mux.Handle("GET /api/admin/documents/{id}/text", service.RequireAdmin(http.HandlerFunc(handlers.Text)))
	mux.Handle("GET /api/admin/documents/{id}/source", service.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handlers.Source(w, r, false) })))
	mux.HandleFunc("GET /api/documents/{id}/source", func(w http.ResponseWriter, r *http.Request) { handlers.Source(w, r, true) })
	mux.HandleFunc("GET /api/passages/{id}/position", func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "Invalid passage ID.", http.StatusBadRequest)
			return
		}
		passage, err := store.PublishedPassage(r.Context(), id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": passage.ID, "documentId": passage.DocumentID,
			"page": passage.Page, "startLine": passage.StartLine, "endLine": passage.EndLine,
			"pageWidth": passage.PageWidth, "pageHeight": passage.PageHeight,
			"words": passage.PDFWords, "section": passage.Section, "content": passage.Content,
		})
	})

	address := env("API_ADDR", ":8081")
	log.Printf("citizen api listening on %s", address)
	if err := http.ListenAndServe(address, service.CORS(securityHeaders(mux))); err != nil {
		log.Fatal(err)
	}
}

func modelClient() llm.Client {
	provider := strings.ToLower(env("LLM_PROVIDER", "openai"))
	apiKey, model, baseURL := os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_MODEL"), ""
	if provider == "openrouter" {
		apiKey, baseURL = os.Getenv("OPENROUTER_API_KEY"), "https://openrouter.ai/api/v1"
	}
	if provider == "gemini" {
		log.Printf("Gemini is configured but not yet supported by the Citizen chat adapter")
		return nil
	}
	if apiKey == "" || model == "" {
		log.Printf("chat model is not configured (provider %q)", provider)
		return nil
	}
	return llm.NewOpenAICompatible(apiKey, model, baseURL)
}

// env returns the configured value or its local-development fallback.
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// securityHeaders adds browser protections before the request reaches a route.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// writeJSON serializes one API response with the expected content type.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
