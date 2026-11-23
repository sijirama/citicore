package documents

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// PDFBox uses the PDF page's top-left coordinate system, in Poppler points.
type PDFBox struct {
	X0 float32 `json:"x0"`
	Y0 float32 `json:"y0"`
	X1 float32 `json:"x1"`
	Y1 float32 `json:"y1"`
}

// PDFWord lets a later viewer locate a quote inside a cited passage.
type PDFWord struct {
	Text string `json:"text"`
	Line int    `json:"line"`
	Box  PDFBox `json:"box"`
}

type pdfLine struct {
	Number int
	Text   string
	Words  []PDFWord
}

type pdfBlock struct{ Lines []pdfLine }
type pdfPage struct {
	Width  float32
	Height float32
	Blocks []pdfBlock
}

// ChunkPDF builds passages from the same positioned words that will anchor highlights.
func ChunkPDF(ctx context.Context, documentID uuid.UUID, title, version string, data []byte) ([]Chunk, error) {
	cmd := exec.CommandContext(ctx, "pdftotext", "-bbox-layout", "-", "-")
	cmd.Stdin = bytes.NewReader(data)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("extract positioned PDF text: %w", err)
	}
	pages, err := parsePDFLayout(bytes.NewReader(output))
	if err != nil {
		return nil, err
	}
	result := make([]Chunk, 0)
	section := ""
	var content strings.Builder
	var words []PDFWord
	var startLine, endLine, currentPage int
	var pageWidth, pageHeight float32
	flush := func() {
		text := strings.TrimSpace(content.String())
		content.Reset()
		if text == "" {
			words = nil
			return
		}
		searchText := title
		if version != "" {
			searchText += " (" + version + ")"
		}
		if section != "" {
			searchText += " — " + section
		}
		result = append(result, Chunk{
			ID: uuid.New(), DocumentID: documentID, Ordinal: len(result), Section: section,
			Page: currentPage, StartLine: startLine, EndLine: endLine,
			PageWidth: pageWidth, PageHeight: pageHeight, PDFWords: words,
			Content: text, SearchText: searchText + "\n" + text,
		})
		words = nil
	}
	for pageIndex, page := range pages {
		for _, block := range page.Blocks {
			if len(block.Lines) == 0 {
				continue
			}
			if headingPattern.MatchString(block.Lines[0].Text) {
				flush()
				section = block.Lines[0].Text
			}
			for lineIndex, line := range block.Lines {
				separator := "\n"
				if lineIndex == 0 {
					separator = "\n\n"
				}
				if content.Len() == 0 {
					separator = ""
				} else if content.Len()+len(separator)+len(line.Text) > 2400 {
					flush()
					separator = ""
				}
				if content.Len() == 0 {
					currentPage = pageIndex + 1
					startLine = line.Number
					pageWidth, pageHeight = page.Width, page.Height
				}
				content.WriteString(separator)
				content.WriteString(line.Text)
				words = append(words, line.Words...)
				endLine = line.Number
			}
		}
		flush()
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("PDF has no positioned text; OCR is required")
	}
	return result, nil
}

func parsePDFLayout(source io.Reader) ([]pdfPage, error) {
	decoder := xml.NewDecoder(source)
	pages := make([]pdfPage, 0)
	var page pdfPage
	var block pdfBlock
	var line pdfLine
	lineNumber := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse positioned PDF text: %w", err)
		}
		switch item := token.(type) {
		case xml.StartElement:
			switch item.Name.Local {
			case "page":
				page = pdfPage{}
				page.Width, err = pdfAttribute(item.Attr, "width")
				if err == nil {
					page.Height, err = pdfAttribute(item.Attr, "height")
				}
				if err != nil || page.Width <= 0 || page.Height <= 0 {
					return nil, fmt.Errorf("PDF page has invalid dimensions: %v", err)
				}
				lineNumber = 0
			case "block":
				block = pdfBlock{}
			case "line":
				lineNumber++
				line = pdfLine{Number: lineNumber}
			case "word":
				box, err := pdfBox(item.Attr)
				if err != nil {
					return nil, err
				}
				var value string
				if err := decoder.DecodeElement(&value, &item); err != nil {
					return nil, fmt.Errorf("read positioned PDF word: %w", err)
				}
				value = strings.TrimSpace(value)
				if value != "" {
					line.Words = append(line.Words, PDFWord{Text: value, Line: lineNumber, Box: box})
				}
			}
		case xml.EndElement:
			switch item.Name.Local {
			case "line":
				if len(line.Words) > 0 {
					parts := make([]string, len(line.Words))
					for i, word := range line.Words {
						parts[i] = word.Text
					}
					line.Text = strings.Join(parts, " ")
					block.Lines = append(block.Lines, line)
				}
			case "block":
				if len(block.Lines) > 0 {
					page.Blocks = append(page.Blocks, block)
				}
			case "page":
				pages = append(pages, page)
			}
		}
	}
	return pages, nil
}

func pdfAttribute(attributes []xml.Attr, name string) (float32, error) {
	for _, attribute := range attributes {
		if attribute.Name.Local == name {
			value, err := strconv.ParseFloat(attribute.Value, 32)
			if err != nil {
				return 0, fmt.Errorf("invalid PDF %s: %w", name, err)
			}
			return float32(value), nil
		}
	}
	return 0, fmt.Errorf("missing PDF %s", name)
}

func pdfBox(attributes []xml.Attr) (PDFBox, error) {
	var box PDFBox
	var err error
	box.X0, err = pdfAttribute(attributes, "xMin")
	if err == nil {
		box.Y0, err = pdfAttribute(attributes, "yMin")
	}
	if err == nil {
		box.X1, err = pdfAttribute(attributes, "xMax")
	}
	if err == nil {
		box.Y1, err = pdfAttribute(attributes, "yMax")
	}
	if err != nil || box.X1 <= box.X0 || box.Y1 <= box.Y0 {
		return PDFBox{}, fmt.Errorf("invalid positioned PDF word box: %v", err)
	}
	return box, nil
}
