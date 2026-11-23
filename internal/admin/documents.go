package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"citizen/core/internal/documents"
	"citizen/core/internal/ingest"
	"github.com/google/uuid"
)

type Documents struct {
	Store    *documents.Store
	Progress *documents.Progress
	Objects  *documents.Objects
	Workflow *ingest.Workflow
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}

func (h *Documents) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.Store.List(r.Context())
	if err != nil {
		fail(w, 500, "Could not load documents.")
		return
	}
	respond(w, 200, items)
}
func (h *Documents) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		fail(w, 400, "Invalid document ID.")
		return
	}
	doc, err := h.Store.Get(r.Context(), id)
	if err != nil {
		fail(w, 404, "Document not found.")
		return
	}
	if doc.Status == "processing" || doc.Status == "failed" {
		doc.Steps, err = h.Progress.Steps(r.Context(), id)
		if err != nil {
			fail(w, 500, "Could not load processing progress.")
			return
		}
	}
	respond(w, 200, doc)
}
func (h *Documents) Upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 22<<20)
	if err := r.ParseMultipartForm(21 << 20); err != nil {
		fail(w, 400, "Upload a file smaller than 20 MB.")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "Choose a document file.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 20<<20+1))
	if err != nil || len(data) > 20<<20 {
		fail(w, 400, "File must be smaller than 20 MB.")
		return
	}
	contentType := ""
	switch strings.ToLower(filepath.Ext(header.Filename)) {
	case ".pdf":
		contentType = "application/pdf"
	case ".docx":
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".txt":
		contentType = "text/plain"
	default:
		fail(w, 400, "Use a PDF, DOCX, or TXT file.")
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" || len(title) > 300 {
		fail(w, 400, "Give the document a title.")
		return
	}
	sourceURL := strings.TrimSpace(r.FormValue("sourceUrl"))
	if sourceURL != "" && !strings.HasPrefix(sourceURL, "https://") {
		fail(w, 400, "Source URL must use HTTPS.")
		return
	}
	id := uuid.New()
	name := filepath.Base(header.Filename)
	key := fmt.Sprintf("documents/%s/%s", id, name)
	if err := h.Objects.Put(r.Context(), key, contentType, data); err != nil {
		fail(w, 502, "Could not store file in R2.")
		return
	}
	digest := sha256.Sum256(data)
	doc := documents.Document{ID: id, Title: title, SourceURL: sourceURL, VersionLabel: strings.TrimSpace(r.FormValue("versionLabel")), ObjectKey: key, FileName: name, ContentType: contentType, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(data)), Status: "processing"}
	if err := h.Store.Create(r.Context(), doc); err != nil {
		_ = h.Objects.Delete(r.Context(), key)
		fail(w, 500, "Could not create document record.")
		return
	}
	if err := h.Workflow.Enqueue(id, "extract"); err != nil {
		_ = h.Store.FinishStep(r.Context(), id, "extract", err)
		_ = h.Progress.Finish(r.Context(), id, "extract", err)
		fail(w, 503, "Document saved, but queueing failed. Retry extraction from admin.")
		return
	}
	item, _ := h.Store.Get(r.Context(), id)
	respond(w, 201, item)
}
func (h *Documents) Action(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		fail(w, 400, "Invalid document ID.")
		return
	}
	action := r.PathValue("action")
	switch action {
	case "retry":
		var input struct {
			Step string `json:"step"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			fail(w, 400, "Choose a step to retry.")
			return
		}
		if err := h.Store.Retry(r.Context(), id, input.Step); err != nil {
			fail(w, 409, err.Error())
			return
		}
		if err := h.Progress.ResetFrom(r.Context(), id, input.Step); err != nil {
			_ = h.Store.FinishStep(r.Context(), id, input.Step, err)
			fail(w, 500, "Could not reset progress.")
			return
		}
		if err := h.Workflow.Enqueue(id, input.Step); err != nil {
			_ = h.Store.FinishStep(r.Context(), id, input.Step, err)
			_ = h.Progress.Finish(r.Context(), id, input.Step, err)
			fail(w, 503, "Could not queue retry.")
			return
		}
	case "publish":
		if err := h.Store.Publish(r.Context(), id); err != nil {
			fail(w, 409, err.Error())
			return
		}
	case "archive":
		if err := h.Store.Archive(r.Context(), id); err != nil {
			fail(w, 409, err.Error())
			return
		}
	default:
		fail(w, 404, "Unknown action.")
		return
	}
	item, err := h.Store.Get(r.Context(), id)
	if err != nil {
		fail(w, 500, "Could not reload document.")
		return
	}
	respond(w, 200, item)
}
func (h *Documents) Source(w http.ResponseWriter, r *http.Request, public bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		fail(w, 400, "Invalid document ID.")
		return
	}
	doc, err := h.Store.Get(r.Context(), id)
	if err != nil || public && doc.Status != "published" {
		fail(w, 404, "Document not found.")
		return
	}
	body, err := h.Objects.Get(r.Context(), doc.ObjectKey)
	if err != nil {
		fail(w, 502, "Could not load source file.")
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", doc.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", strings.ReplaceAll(doc.FileName, "\"", "")))
	_, _ = io.Copy(w, body)
}

func (h *Documents) Chunks(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		fail(w, 400, "Invalid document ID.")
		return
	}
	if _, err := h.Store.Get(r.Context(), id); err != nil {
		fail(w, 404, "Document not found.")
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}
	items, err := h.Store.InspectChunks(r.Context(), id, offset, 40)
	if err != nil {
		fail(w, 500, "Could not load passages.")
		return
	}
	respond(w, 200, items)
}
func (h *Documents) Text(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		fail(w, 400, "Invalid document ID.")
		return
	}
	if _, err := h.Store.Get(r.Context(), id); err != nil {
		fail(w, 404, "Document not found.")
		return
	}
	content, err := h.Store.ExtractedText(r.Context(), id)
	if err != nil {
		fail(w, 500, "Could not load extracted text.")
		return
	}
	if content == "" {
		fail(w, 409, "Text is not ready yet.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, content)
}
