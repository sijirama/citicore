package main

import (
	"context"
	"log"
	"os"

	"citizen/core/internal/documents"
	"citizen/core/internal/ingest"
	"citizen/core/internal/llm"
	"github.com/hibiken/asynq"
	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	_ = godotenv.Load()
	ctx := context.Background()
	db, err := gorm.Open(postgres.Open(os.Getenv("DATABASE_URL")), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	if err := documents.AutoMigrate(db); err != nil {
		log.Fatal(err)
	}
	objects, err := documents.NewObjects(ctx)
	if err != nil {
		log.Fatal(err)
	}
	redis, err := ingest.RedisOption()
	if err != nil {
		log.Fatal(err)
	}
	client := asynq.NewClient(redis)
	defer client.Close()
	classifier, err := llm.FromEnv()
	if err != nil {
		log.Fatal(err)
	}
	progress, err := documents.NewProgress(os.Getenv("REDIS_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer progress.Close()
	workflow := &ingest.Workflow{
		Store: documents.NewStore(db), Progress: progress, Objects: objects,
		Embedder:   documents.NewEmbedder(os.Getenv("OPENAI_API_KEY"), os.Getenv("EMBEDDING_MODEL")),
		Classifier: classifier, Queue: client,
	}
	server := asynq.NewServer(redis, asynq.Config{Concurrency: 3, Queues: map[string]int{"ingest": 1}})
	mux := asynq.NewServeMux()
	mux.HandleFunc(ingest.TaskType, workflow.Handle)
	log.Print("citizen ingestion worker listening on ingest queue")
	if err := server.Run(mux); err != nil {
		log.Fatal(err)
	}
}
