package documents

import (
    "archive/zip"
    "bytes"
    "context"
    "encoding/xml"
    "fmt"
    "io"
    "os/exec"
    "strings"
    "unicode/utf8"
)

func Extract(ctx context.Context, contentType string, data []byte) (string, error) {
    switch contentType {
    case "application/pdf":
        cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-", "-")
        cmd.Stdin = bytes.NewReader(data)
        output, err := cmd.Output()
        if err != nil { return "", fmt.Errorf("extract PDF with pdftotext: %w", err) }
        if len(bytes.TrimSpace(output)) < 20 { return "", fmt.Errorf("PDF has no readable text; OCR or a text-based PDF is required") }
        return strings.TrimSpace(string(output)), nil
    case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
        return extractDOCX(data)
    case "text/plain":
        if !utf8.Valid(data) { return "", fmt.Errorf("plain text must be UTF-8") }
        text := strings.TrimSpace(string(data))
        if len(text) < 20 { return "", fmt.Errorf("document contains too little text") }
        return text, nil
    default:
        return "", fmt.Errorf("unsupported content type %q", contentType)
    }
}

func extractDOCX(data []byte) (string, error) {
    archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
    if err != nil { return "", fmt.Errorf("open DOCX: %w", err) }
    for _, file := range archive.File {
        if file.Name != "word/document.xml" { continue }
        source, err := file.Open()
        if err != nil { return "", err }
        defer source.Close()
        decoder := xml.NewDecoder(source)
        var result strings.Builder
        for {
            token, err := decoder.Token()
            if err == io.EOF { break }
            if err != nil { return "", err }
            start, ok := token.(xml.StartElement)
            if !ok { continue }
            switch start.Name.Local {
            case "t":
                var value string
                if err := decoder.DecodeElement(&value, &start); err != nil { return "", err }
                result.WriteString(value)
            case "p":
                if result.Len() > 0 { result.WriteString("\n") }
            case "tab":
                result.WriteString(" ")
            }
        }
        text := strings.TrimSpace(result.String())
        if len(text) < 20 { return "", fmt.Errorf("DOCX contains too little text") }
        return text, nil
    }
    return "", fmt.Errorf("DOCX has no document.xml")
}
