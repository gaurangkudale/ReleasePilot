package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
)

const (
	pdfPageWidth  = 612.0
	pdfPageHeight = 792.0
	pdfMargin     = 42.0
	pdfBottom     = 54.0
	pdfContentW   = pdfPageWidth - (pdfMargin * 2)
)

type pdfColor struct {
	r float64
	g float64
	b float64
}

var (
	pdfInk      = pdfColor{0.07, 0.10, 0.18}
	pdfMuted    = pdfColor{0.36, 0.42, 0.52}
	pdfLine     = pdfColor{0.85, 0.88, 0.93}
	pdfPanel    = pdfColor{0.97, 0.98, 1.00}
	pdfWhite    = pdfColor{1.00, 1.00, 1.00}
	pdfNavy     = pdfColor{0.07, 0.12, 0.24}
	pdfBlue     = pdfColor{0.16, 0.31, 0.75}
	pdfGreen    = pdfColor{0.05, 0.50, 0.31}
	pdfAmber    = pdfColor{0.78, 0.42, 0.04}
	pdfRed      = pdfColor{0.78, 0.12, 0.17}
	pdfSlate    = pdfColor{0.38, 0.45, 0.55}
	pdfSoftBlue = pdfColor{0.92, 0.95, 1.00}
)

type pdfReport struct {
	pages   []string
	content strings.Builder
	page    int
	y       float64
}

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
	item = normalizeRelease(item)

	doc := &pdfReport{}
	doc.newPage()
	doc.drawIntro(item)
	doc.drawScoreCards(item)
	doc.drawTextCard("Executive Summary", emptyAs(item.Summary, "No release summary was generated."), pdfBlue)
	doc.drawTextCard("Release Recommendation", emptyAs(item.Recommendation, "No recommendation was generated."), pdfDecisionColor(item.Decision))
	doc.drawValidations(item.Validations)
	doc.drawAgents(item.Agents)
	doc.drawRisks(item.Risks)
	doc.drawBlastRadius(item.BlastRadius)
	doc.drawChangedFiles(item.ChangedFiles)
	doc.drawList("Rollback Plan", item.RollbackPlan, true)
	doc.drawList("Release Notes", item.ReleaseNotes, false)

	return doc.bytes()
}

func (p *pdfReport) newPage() {
	if p.page > 0 {
		p.drawFooter()
		p.pages = append(p.pages, p.content.String())
		p.content.Reset()
	}
	p.page++
	p.y = pdfPageHeight - 118

	p.fillRect(0, pdfPageHeight-92, pdfPageWidth, 92, pdfNavy)
	p.fillRect(pdfMargin, pdfPageHeight-61, 22, 22, pdfBlue)
	p.text("F2", 17, pdfMargin+32, pdfPageHeight-45, "ReleasePilot", pdfWhite)
	p.text("F1", 9, pdfMargin+32, pdfPageHeight-62, "AI Release Readiness Report", pdfColor{0.78, 0.84, 0.95})
	p.textRight("F1", 8.5, pdfPageWidth-pdfMargin, pdfPageHeight-45, fmt.Sprintf("Page %d", p.page), pdfColor{0.78, 0.84, 0.95})
}

func (p *pdfReport) bytes() []byte {
	if p.page == 0 {
		p.newPage()
	}
	p.drawFooter()
	p.pages = append(p.pages, p.content.String())
	return renderPDF(p.pages)
}

func (p *pdfReport) drawIntro(item release) {
	p.ensureSpace(72)
	title := emptyAs(item.Name, "Release report")
	p.text("F2", 18, pdfMargin, p.y, truncateText(title, 58), pdfInk)
	if !item.CreatedAt.IsZero() {
		p.textRight("F1", 8.5, pdfPageWidth-pdfMargin, p.y, "Generated "+item.CreatedAt.UTC().Format("02 Jan 2006 15:04 UTC"), pdfMuted)
	}
	p.y -= 19

	repoLine := fmt.Sprintf("%s / %s", emptyAs(item.Provider, "provider"), emptyAs(item.Repository, "repository"))
	branchLine := fmt.Sprintf("%s -> %s", emptyAs(item.ReleaseBranch, "release branch"), emptyAs(item.BaseBranch, "base branch"))
	meta := fmt.Sprintf("Repository: %s    Branch: %s    Target: %s", repoLine, branchLine, emptyAs(item.Target, "Production"))
	for _, line := range wrapText(meta, pdfContentW, 8.8) {
		p.text("F1", 8.8, pdfMargin, p.y, line, pdfMuted)
		p.y -= 11
	}
	p.y -= 8
}

func (p *pdfReport) drawScoreCards(item release) {
	cardH := 70.0
	gap := 10.0
	cardW := (pdfContentW - (gap * 3)) / 4
	p.ensureSpace(cardH + 28)
	top := p.y

	cards := []struct {
		label string
		value string
		color pdfColor
	}{
		{"Decision", emptyAs(item.Decision, "UNKNOWN"), pdfDecisionColor(item.Decision)},
		{"Health", fmt.Sprintf("%d/100", item.Health), pdfHealthColor(item.Health)},
		{"Provider", strings.ToUpper(emptyAs(item.Provider, "unknown")), pdfBlue},
		{"Branch", emptyAs(item.ReleaseBranch, "release branch"), pdfSlate},
	}

	for i, card := range cards {
		x := pdfMargin + float64(i)*(cardW+gap)
		p.drawMetricCard(x, top, cardW, cardH, card.label, card.value, card.color)
		if card.label == "Health" {
			p.drawHealthBar(x+12, top-cardH+15, cardW-24, item.Health, card.color)
		}
	}
	p.y = top - cardH - 18
}

func (p *pdfReport) drawMetricCard(x, top, w, h float64, label, value string, color pdfColor) {
	p.fillRect(x, top-h, w, h, pdfWhite)
	p.strokeRect(x, top-h, w, h, pdfLine)
	p.fillRect(x, top-h, 4, h, color)
	p.text("F1", 7.5, x+12, top-17, strings.ToUpper(label), pdfMuted)

	valueSize := 14.0
	if len(value) > 18 {
		valueSize = 10.5
	}
	lines := limitLines(wrapText(value, w-24, valueSize), 2)
	y := top - 36
	for _, line := range lines {
		p.text("F2", valueSize, x+12, y, line, pdfInk)
		y -= valueSize + 2
	}
}

func (p *pdfReport) drawHealthBar(x, y, w float64, health int, color pdfColor) {
	if health < 0 {
		health = 0
	}
	if health > 100 {
		health = 100
	}
	p.fillRect(x, y, w, 6, pdfColor{0.90, 0.92, 0.96})
	p.fillRect(x, y, w*(float64(health)/100), 6, color)
}

func (p *pdfReport) drawTextCard(title, body string, accent pdfColor) {
	lines := wrapText(body, pdfContentW-28, 9.5)
	if len(lines) == 0 {
		lines = []string{"No data available."}
	}

	for len(lines) > 0 {
		available := int((p.y - pdfBottom - 44) / 13)
		if available < 3 {
			p.newPage()
			available = int((p.y - pdfBottom - 44) / 13)
		}
		take := len(lines)
		if take > available {
			take = available
		}
		chunk := lines[:take]
		height := 36 + float64(len(chunk))*13
		top := p.y

		p.fillRect(pdfMargin, top-height, pdfContentW, height, pdfPanel)
		p.strokeRect(pdfMargin, top-height, pdfContentW, height, pdfLine)
		p.fillRect(pdfMargin, top-height, 5, height, accent)
		p.text("F2", 12.5, pdfMargin+16, top-17, title, pdfInk)
		y := top - 35
		for _, line := range chunk {
			p.text("F1", 9.5, pdfMargin+16, y, line, pdfInk)
			y -= 13
		}
		p.y = top - height - 14

		lines = lines[take:]
		if len(lines) > 0 {
			title += " (cont.)"
		}
	}
}

func (p *pdfReport) drawValidations(validations []validation) {
	p.sectionTitle("Readiness Checks")
	if len(validations) == 0 {
		p.drawEmpty("No validation checks were recorded.")
		return
	}

	for _, item := range validations {
		evidence := emptyAs(item.Evidence, "No evidence recorded")
		evidenceLines := limitLines(wrapText(evidence, pdfContentW-175, 8.6), 3)
		height := maxFloat(42, 25+float64(len(evidenceLines))*11)
		p.ensureSpace(height + 7)

		top := p.y
		p.fillRect(pdfMargin, top-height, pdfContentW, height, pdfWhite)
		p.strokeRect(pdfMargin, top-height, pdfContentW, height, pdfLine)
		p.badge(pdfMargin+12, top-13, 66, 16, item.Status, pdfStatusColor(item.Status))
		p.text("F2", 9.5, pdfMargin+92, top-19, truncateText(item.Name, 38), pdfInk)
		y := top - 33
		for _, line := range evidenceLines {
			p.text("F1", 8.6, pdfMargin+92, y, line, pdfMuted)
			y -= 11
		}
		p.y = top - height - 7
	}
}

func (p *pdfReport) drawAgents(agents []agentReport) {
	p.sectionTitle("Specialized Risk Agents")
	if len(agents) == 0 {
		p.drawEmpty("No specialized agents ran for this report.")
		return
	}

	gap := 12.0
	cardW := (pdfContentW - gap) / 2
	for i := 0; i < len(agents); i += 2 {
		leftH := agentCardHeight(agents[i], cardW)
		rowH := leftH
		if i+1 < len(agents) {
			rowH = maxFloat(rowH, agentCardHeight(agents[i+1], cardW))
		}
		p.ensureSpace(rowH + 11)
		top := p.y
		p.drawAgentCard(pdfMargin, top, cardW, rowH, agents[i])
		if i+1 < len(agents) {
			p.drawAgentCard(pdfMargin+cardW+gap, top, cardW, rowH, agents[i+1])
		}
		p.y = top - rowH - 11
	}
}

func agentCardHeight(agent agentReport, w float64) float64 {
	lines := limitLines(wrapText(agent.Summary, w-24, 8.5), 3)
	height := 61 + float64(len(lines))*10.5
	if len(agent.Findings) > 0 {
		height += 14
	}
	return maxFloat(88, height)
}

func (p *pdfReport) drawAgentCard(x, top, w, h float64, agent agentReport) {
	color := pdfAgentColor(agent.Status)
	p.fillRect(x, top-h, w, h, pdfWhite)
	p.strokeRect(x, top-h, w, h, pdfLine)
	p.fillRect(x, top-h, 5, h, color)
	p.text("F2", 10.5, x+14, top-17, truncateText(agent.Name, 26), pdfInk)
	p.badge(x+w-68, top-12, 54, 15, agent.Status, color)
	p.text("F1", 7.8, x+14, top-31, truncateText(agent.Domain, 31), pdfMuted)
	p.textRight("F1", 7.8, x+w-14, top-31, fmt.Sprintf("%d%% confidence", agent.Confidence), pdfMuted)

	y := top - 48
	for _, line := range limitLines(wrapText(agent.Summary, w-24, 8.5), 3) {
		p.text("F1", 8.5, x+14, y, line, pdfInk)
		y -= 10.5
	}
	if len(agent.Findings) > 0 {
		p.text("F2", 8.2, x+14, top-h+14, truncateText(fmt.Sprintf("%d finding(s): %s", len(agent.Findings), agent.Findings[0].Title), 42), pdfAgentColor(agent.Status))
	}
}

func (p *pdfReport) drawRisks(risks []risk) {
	p.sectionTitle("Risk Findings")
	if len(risks) == 0 {
		p.drawEmpty("No risk findings were recorded.")
		return
	}

	for _, item := range risks {
		detail := strings.TrimSpace(item.Impact + " | " + item.Evidence)
		lines := limitLines(wrapText(detail, pdfContentW-125, 8.6), 3)
		height := maxFloat(52, 33+float64(len(lines))*11)
		p.ensureSpace(height + 8)

		top := p.y
		color := pdfSeverityColor(item.Level)
		p.fillRect(pdfMargin, top-height, pdfContentW, height, pdfWhite)
		p.strokeRect(pdfMargin, top-height, pdfContentW, height, pdfLine)
		p.fillRect(pdfMargin, top-height, 5, height, color)
		p.badge(pdfMargin+14, top-15, 72, 16, item.Level, color)
		p.text("F2", 10, pdfMargin+104, top-19, truncateText(item.Title, 58), pdfInk)
		y := top - 35
		for _, line := range lines {
			p.text("F1", 8.6, pdfMargin+104, y, line, pdfMuted)
			y -= 11
		}
		p.y = top - height - 8
	}
}

func (p *pdfReport) drawBlastRadius(graph blastRadiusGraph) {
	p.sectionTitle("Blast Radius")
	if len(graph.Nodes) == 0 {
		p.drawEmpty("No blast-radius graph was generated.")
		return
	}

	p.drawTableHeader([]pdfColumn{
		{label: "Severity", x: pdfMargin + 12, w: 70},
		{label: "Node", x: pdfMargin + 92, w: 128},
		{label: "Kind", x: pdfMargin + 230, w: 70},
		{label: "Evidence", x: pdfMargin + 306, w: pdfContentW - 318},
	})
	for _, node := range graph.Nodes {
		evidenceLines := limitLines(wrapText(emptyAs(node.Evidence, "No evidence"), pdfContentW-318, 8.3), 2)
		height := maxFloat(34, 21+float64(len(evidenceLines))*10.5)
		p.ensureSpace(height + 3)

		top := p.y
		color := pdfSeverityColor(node.Severity)
		p.fillRect(pdfMargin, top-height, pdfContentW, height, pdfWhite)
		p.strokeRect(pdfMargin, top-height, pdfContentW, height, pdfLine)
		p.badge(pdfMargin+12, top-10, 68, 15, node.Severity, color)
		p.text("F2", 8.7, pdfMargin+92, top-17, truncateText(node.Label, 26), pdfInk)
		p.text("F1", 8.3, pdfMargin+230, top-17, truncateText(node.Kind, 15), pdfMuted)
		y := top - 17
		for _, line := range evidenceLines {
			p.text("F1", 8.3, pdfMargin+306, y, line, pdfMuted)
			y -= 10.5
		}
		p.y = top - height - 3
	}
}

func (p *pdfReport) drawChangedFiles(files []changedFile) {
	p.sectionTitle("Changed Files")
	if len(files) == 0 {
		p.drawEmpty("No changed-file compare data was available.")
		return
	}

	p.drawTableHeader([]pdfColumn{
		{label: "Status", x: pdfMargin + 12, w: 58},
		{label: "Path", x: pdfMargin + 82, w: pdfContentW - 185},
		{label: "Delta", x: pdfPageWidth - pdfMargin - 86, w: 74},
	})
	limit := 12
	if len(files) < limit {
		limit = len(files)
	}
	for _, file := range files[:limit] {
		pathLines := limitLines(wrapText(file.Path, pdfContentW-190, 8.3), 2)
		height := maxFloat(32, 20+float64(len(pathLines))*10.5)
		p.ensureSpace(height + 3)

		top := p.y
		p.fillRect(pdfMargin, top-height, pdfContentW, height, pdfWhite)
		p.strokeRect(pdfMargin, top-height, pdfContentW, height, pdfLine)
		p.text("F2", 8.2, pdfMargin+12, top-16, truncateText(emptyAs(file.Status, "modified"), 12), pdfBlue)
		y := top - 16
		for _, line := range pathLines {
			p.text("F3", 8.3, pdfMargin+82, y, line, pdfInk)
			y -= 10.5
		}
		p.textRight("F1", 8.3, pdfPageWidth-pdfMargin-12, top-16, fmt.Sprintf("+%d / -%d", file.Additions, file.Deletions), pdfMuted)
		p.y = top - height - 3
	}
	if len(files) > limit {
		p.ensureSpace(18)
		p.text("F1", 8.5, pdfMargin+12, p.y, fmt.Sprintf("%d additional changed files omitted from this PDF summary.", len(files)-limit), pdfMuted)
		p.y -= 16
	}
}

func (p *pdfReport) drawList(title string, items []string, numbered bool) {
	p.sectionTitle(title)
	if len(items) == 0 {
		p.drawEmpty("No entries were recorded.")
		return
	}

	for i, item := range items {
		lines := wrapText(item, pdfContentW-45, 9)
		height := maxFloat(28, 14+float64(len(lines))*12)
		p.ensureSpace(height + 4)
		top := p.y
		marker := "-"
		if numbered {
			marker = fmt.Sprintf("%d", i+1)
		}
		p.fillRect(pdfMargin, top-height, pdfContentW, height, pdfWhite)
		p.strokeRect(pdfMargin, top-height, pdfContentW, height, pdfLine)
		p.badge(pdfMargin+12, top-10, 18, 15, marker, pdfBlue)
		y := top - 16
		for _, line := range lines {
			p.text("F1", 9, pdfMargin+43, y, line, pdfInk)
			y -= 12
		}
		p.y = top - height - 4
	}
}

func (p *pdfReport) sectionTitle(title string) {
	p.ensureSpace(36)
	p.y -= 3
	p.fillRect(pdfMargin, p.y-18, 4, 19, pdfBlue)
	p.text("F2", 13, pdfMargin+13, p.y-13, title, pdfInk)
	p.line(pdfMargin, p.y-27, pdfPageWidth-pdfMargin, p.y-27, pdfLine)
	p.y -= 39
}

func (p *pdfReport) drawEmpty(message string) {
	p.ensureSpace(36)
	top := p.y
	p.fillRect(pdfMargin, top-32, pdfContentW, 32, pdfPanel)
	p.strokeRect(pdfMargin, top-32, pdfContentW, 32, pdfLine)
	p.text("F1", 8.8, pdfMargin+12, top-19, message, pdfMuted)
	p.y = top - 42
}

func (p *pdfReport) ensureSpace(height float64) {
	if p.y-height < pdfBottom {
		p.newPage()
	}
}

func (p *pdfReport) drawFooter() {
	p.line(pdfMargin, 38, pdfPageWidth-pdfMargin, 38, pdfLine)
	p.text("F1", 7.5, pdfMargin, 24, "Generated by ReleasePilot. Validate critical systems before production deployment.", pdfMuted)
	p.textRight("F1", 7.5, pdfPageWidth-pdfMargin, 24, fmt.Sprintf("Page %d", p.page), pdfMuted)
}

func (p *pdfReport) text(font string, size, x, y float64, value string, color pdfColor) {
	fmt.Fprintf(&p.content, "BT\n%.3f %.3f %.3f rg\n/%s %.2f Tf\n%.2f %.2f Td\n(%s) Tj\nET\n", color.r, color.g, color.b, font, size, x, y, escapePDF(value))
}

func (p *pdfReport) textRight(font string, size, right, y float64, value string, color pdfColor) {
	p.text(font, size, right-approxTextWidth(value, size), y, value, color)
}

func (p *pdfReport) fillRect(x, y, w, h float64, color pdfColor) {
	fmt.Fprintf(&p.content, "q\n%.3f %.3f %.3f rg\n%.2f %.2f %.2f %.2f re f\nQ\n", color.r, color.g, color.b, x, y, w, h)
}

func (p *pdfReport) strokeRect(x, y, w, h float64, color pdfColor) {
	fmt.Fprintf(&p.content, "q\n%.3f %.3f %.3f RG\n0.75 w\n%.2f %.2f %.2f %.2f re S\nQ\n", color.r, color.g, color.b, x, y, w, h)
}

func (p *pdfReport) line(x1, y1, x2, y2 float64, color pdfColor) {
	fmt.Fprintf(&p.content, "q\n%.3f %.3f %.3f RG\n0.65 w\n%.2f %.2f m\n%.2f %.2f l\nS\nQ\n", color.r, color.g, color.b, x1, y1, x2, y2)
}

func (p *pdfReport) badge(x, top, w, h float64, label string, color pdfColor) {
	p.fillRect(x, top-h, w, h, color)
	p.text("F2", 6.8, x+5, top-10.5, truncateText(strings.ToUpper(emptyAs(label, "N/A")), int(w/4.4)), pdfWhite)
}

type pdfColumn struct {
	label string
	x     float64
	w     float64
}

func (p *pdfReport) drawTableHeader(columns []pdfColumn) {
	p.ensureSpace(24)
	top := p.y
	p.fillRect(pdfMargin, top-22, pdfContentW, 22, pdfSoftBlue)
	p.strokeRect(pdfMargin, top-22, pdfContentW, 22, pdfLine)
	for _, column := range columns {
		p.text("F2", 7.4, column.x, top-14, strings.ToUpper(column.label), pdfBlue)
	}
	p.y = top - 25
}

func renderPDF(pageContents []string) []byte {
	if len(pageContents) == 0 {
		pageContents = []string{""}
	}

	objects := make([]string, 5)
	objects[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	objects[2] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
	objects[3] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold >>"
	objects[4] = "<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>"

	kids := make([]string, 0, len(pageContents))
	for i, content := range pageContents {
		pageObjectID := 6 + (i * 2)
		contentObjectID := pageObjectID + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", pageObjectID))
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R /F3 5 0 R >> >> /Contents %d 0 R >>", pdfPageWidth, pdfPageHeight, contentObjectID),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		)
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pageContents))

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

func wrapText(value string, width, size float64) []string {
	cleaned := cleanPDFText(value)
	if cleaned == "" {
		return nil
	}
	maxChars := int(width / (size * 0.50))
	if maxChars < 12 {
		maxChars = 12
	}

	words := strings.Fields(cleaned)
	lines := []string{}
	current := ""
	for _, word := range words {
		if len(word) > maxChars {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			for len(word) > maxChars {
				cut := maxChars - 1
				lines = append(lines, word[:cut]+"-")
				word = word[cut:]
			}
		}
		if current == "" {
			current = word
			continue
		}
		if len(current)+len(word)+1 > maxChars {
			lines = append(lines, current)
			current = word
		} else {
			current += " " + word
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func limitLines(lines []string, limit int) []string {
	if limit <= 0 || len(lines) <= limit {
		return lines
	}
	result := append([]string{}, lines[:limit]...)
	result[len(result)-1] = strings.TrimRight(result[len(result)-1], ". ") + "..."
	return result
}

func approxTextWidth(value string, size float64) float64 {
	return float64(len(cleanPDFText(value))) * size * 0.50
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func pdfDecisionColor(decision string) pdfColor {
	switch decision {
	case "GO":
		return pdfGreen
	case "NO-GO":
		return pdfRed
	default:
		return pdfAmber
	}
}

func pdfHealthColor(health int) pdfColor {
	switch {
	case health >= 80:
		return pdfGreen
	case health >= 60:
		return pdfAmber
	default:
		return pdfRed
	}
}

func pdfStatusColor(status string) pdfColor {
	switch status {
	case "Passed", "Clear":
		return pdfGreen
	case "Failed", "Blocked":
		return pdfRed
	case "Warning", "Pending", "Review":
		return pdfAmber
	default:
		return pdfSlate
	}
}

func pdfAgentColor(status string) pdfColor {
	switch status {
	case "Blocked":
		return pdfRed
	case "Review":
		return pdfAmber
	case "Clear":
		return pdfGreen
	default:
		return pdfSlate
	}
}

func pdfSeverityColor(level string) pdfColor {
	switch level {
	case "Critical":
		return pdfRed
	case "High", "Medium":
		return pdfAmber
	case "Low":
		return pdfGreen
	default:
		return pdfSlate
	}
}

func cleanPDFText(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 32 || r > 126 {
			return '-'
		}
		return r
	}, value))
}

func truncateText(value string, limit int) string {
	value = cleanPDFText(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func escapePDF(value string) string {
	return strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`).Replace(cleanPDFText(value))
}

func safeFilename(value string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, value)
}
