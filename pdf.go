package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
)

func (s *store) handlePDF(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	item, ok := s.releases[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "release not found", http.StatusNotFound)
		return
	}
	data := buildPDF(item)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-release-report.pdf"`, safeFilename(id)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func buildPDF(item release) []byte {
	lines := []string{
		"ReleasePilot Release Readiness Report",
		item.Name,
		"Decision: " + item.Decision,
		fmt.Sprintf("Release health: %d/100", item.Health),
		"Repository: " + item.Provider + " / " + item.Repository,
		"Branch: " + item.ReleaseBranch + " -> " + item.BaseBranch,
		"",
		"Summary",
		item.Summary,
		"",
		"Recommendation",
		item.Recommendation,
		"",
		"Risk findings",
	}
	for _, risk := range item.Risks {
		lines = append(lines, fmt.Sprintf("- [%s] %s | %s | %s", risk.Level, risk.Title, risk.Impact, risk.Evidence))
	}
	lines = append(lines, "", "Rollback plan")
	for i, step := range item.RollbackPlan {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, step))
	}
	lines = append(lines, "", "Release notes")
	for _, note := range item.ReleaseNotes {
		lines = append(lines, "- "+note)
	}

	content := "BT\n/F1 16 Tf\n50 790 Td\n"
	for i, line := range wrapPDFLines(lines, 88) {
		if i == 1 {
			content += "/F1 10 Tf\n"
		}
		content += "(" + escapePDF(line) + ") Tj\n0 -14 Td\n"
	}
	content += "ET"

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for i, object := range objects {
		offsets[i+1] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for i := 1; i <= len(objects); i++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF", len(objects)+1, xref)
	return out.Bytes()
}

func wrapPDFLines(lines []string, width int) []string {
	var result []string
	for _, line := range lines {
		words := strings.Fields(line)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		current := words[0]
		for _, word := range words[1:] {
			if len(current)+len(word)+1 > width {
				result = append(result, current)
				current = word
			} else {
				current += " " + word
			}
		}
		result = append(result, current)
	}
	return result
}

func escapePDF(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return '-'
		}
		return r
	}, value)
	return strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`).Replace(value)
}

func safeFilename(value string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, value)
}
