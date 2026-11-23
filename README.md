# Citizen core

The Go API and Asynq worker power Citizen's curated document library and source-backed chat. PostgreSQL with pgvector stores document metadata, passages, and embeddings through GORM. Cloudflare R2 stores the original private files. Redis carries ingestion jobs and temporary processing progress. There is no MinIO service or open-web/news search tool.

## Local setup

1. Set `DATABASE_URL=postgres://citizen:citizen@localhost:5437/citizen?sslmode=disable` and `REDIS_URL=redis://localhost:6382` in `.env` (already included in `.env.example`). Keep your R2, model, and Google OAuth values there too.
2. Run `docker compose up -d postgres redis` here. The Compose project is named `citizen` and uses ports 5437 and 6382 to avoid the other local projects.
3. Run `go run ./cmd/api` and `go run ./cmd/worker` in separate terminals. The API defaults to port 8081. Both processes use GORM AutoMigrate to create or update the document tables at startup.
4. Run the Vite client and sign in to `/admin` with an email in `ADMIN_EMAILS`.

`pdftotext` (Poppler) must be installed for PDF extraction. PDF chunking uses `pdftotext -bbox-layout` so each passage keeps its page, extracted line range, page dimensions, and word boxes for future exact-quote highlighting. DOCX and UTF-8 TXT extraction are built in but have no PDF positions. PDFs that contain only scanned images currently fail extraction and show a retryable error; OCR is not yet part of this flow. Documents ingested before positional chunking need to be reprocessed to gain word boxes.

## Ingestion and review

The admin uploads a PDF, DOCX, or TXT file (up to 20 MB) with a title and optional official URL/version. The API writes the original to R2 and creates a GORM document record. Asynq runs **extract → classify → chunk → embed** as separate jobs. The worker keeps each step’s status, attempt count, and error in a temporary Redis hash; it deletes that hash when ingestion succeeds. Failed jobs retry up to three times automatically, and a failed run keeps progress for seven days so an admin can inspect it and retry. Restarting clears downstream results and removes a document from the published library while it is rebuilt.

After embeddings finish, the document enters `review`. An admin opens the source, checks classification and metadata, and publishes it. Only published documents enter search. Archiving removes a document from search immediately. The admin API is Google-session protected; public source download only serves published files. `POST /api/chat` searches approved passages through the single `search_documents` tool, combines lexical and vector candidates, and exposes citations only when they match retrieved passage IDs.

Admin routes: `GET/POST /api/admin/documents`, `GET /api/admin/documents/{id}`, `GET /api/admin/documents/{id}/source`, `GET /api/admin/documents/{id}/text`, `GET /api/admin/documents/{id}/chunks`, and `POST /api/admin/documents/{id}/{retry|publish|archive}`. Retry accepts JSON `{ "step": "extract" }` (or `classify`, `chunk`, `embed`). Public source route: `GET /api/documents/{id}/source`.

## Tests

Run `go test ./...`. For the database lifecycle integration test, set `CITIZEN_TEST_DATABASE_URL` to the local Postgres DSN above and run `go test ./internal/documents -v`. The test creates and removes its own document and checks publication, retrieval, archiving, and retry. Set `CITIZEN_TEST_REDIS_URL` to the Redis URL above to run the temporary-progress test too.

For answer-quality checks, run `go run ./cmd/eval` (or pass questions as arguments). It writes one JSON object per question with the model's search queries, returned passage IDs/sections/pages/excerpts, and final answer. Use a separate local `DATABASE_URL` with reviewed documents marked published when evaluating before public publication. The first local Constitution run and focused reruns are in `eval-results/`.
