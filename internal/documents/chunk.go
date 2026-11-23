package documents

import (
    "regexp"
    "strings"

    "github.com/google/uuid"
)

var headingPattern = regexp.MustCompile(`(?i)^(?:part|chapter|schedule|section)\s+[ivxlcdm0-9]+\b|^\d+[a-z]?\.\s+[A-Z]`)

// ChunkByStructure preserves page and section context while bounding search passages.
func ChunkByStructure(documentID uuid.UUID, title, version, content string) []Chunk {
    pages := strings.Split(content, "\f")
    result := make([]Chunk, 0)
    section := ""
    var buffer strings.Builder
    currentPage := 1
    appendChunk := func() {
        text := strings.TrimSpace(buffer.String())
        buffer.Reset()
        if text == "" { return }
        context := title
        if version != "" { context += " (" + version + ")" }
        if section != "" { context += " — " + section }
        result = append(result, Chunk{
            ID: uuid.New(), DocumentID: documentID, Ordinal: len(result), Section: section, Page: currentPage,
            Content: text, SearchText: context + "\n" + text,
        })
    }
    for pageIndex, page := range pages {
        blocks := regexp.MustCompile(`\n\s*\n`).Split(page, -1)
        for _, block := range blocks {
            block = strings.TrimSpace(block)
            if block == "" { continue }
            firstLine := strings.TrimSpace(strings.SplitN(block, "\n", 2)[0])
            if headingPattern.MatchString(firstLine) {
                appendChunk()
                section = firstLine
            }
            if buffer.Len()+len(block) > 2400 && buffer.Len() > 0 { appendChunk() }
            if len(block) > 2400 {
                words := strings.Fields(block)
                for _, word := range words {
                    if buffer.Len()+len(word)+1 > 2400 { appendChunk() }
                    if buffer.Len() > 0 { buffer.WriteByte(' ') }
                    buffer.WriteString(word)
                }
            } else {
                if buffer.Len() > 0 { buffer.WriteString("\n\n") }
                buffer.WriteString(block)
            }
        }
        appendChunk()
        currentPage = pageIndex + 2
    }
    return result
}
