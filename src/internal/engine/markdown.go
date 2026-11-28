package engine

import (
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func newConverter() *converter.Converter {
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(
				table.WithCellPaddingBehavior(table.CellPaddingBehaviorMinimal),
				table.WithSkipEmptyRows(true),
			),
		),
	)
	conv.Register.PreRenderer(unwrapLayoutTables, converter.PriorityEarly)
	return conv
}

// unwrapLayoutTables converts HTML tables used for page layout (such as nested
// tables, presentation tables, or borderless non-header tables) into plain
// block rows so they render as readable text instead of wide Markdown tables.
func unwrapLayoutTables(_ converter.Context, doc *html.Node) {
	var tables []*html.Node
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Table {
			tables = append(tables, n)
		}
	}
	collect(doc)
	// Post-order traversal ensures nested tables are unwrapped before their parents.
	for _, tbl := range tables {
		if tbl.Parent != nil && isLayoutTable(tbl) {
			unwrapTable(tbl)
		}
	}
}

func isLayoutTable(tbl *html.Node) bool {
	role := strings.ToLower(strings.TrimSpace(attr(tbl, "role")))
	if role == "presentation" || role == "none" {
		return true
	}
	hasHeader := false
	hasNestedTable := false
	maxCols := 0
	var inspect func(*html.Node)
	inspect = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.DataAtom {
			case atom.Table:
				hasNestedTable = true
				continue
			case atom.Th, atom.Thead, atom.Caption:
				hasHeader = true
			case atom.Tr:
				cols := 0
				for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.DataAtom == atom.Td || cell.DataAtom == atom.Th) {
						if cell.DataAtom == atom.Th {
							hasHeader = true
						}
						if hasVisibleContent(cell) {
							cols++
						}
					}
				}
				if cols > maxCols {
					maxCols = cols
				}
			}
			inspect(c)
		}
	}
	inspect(tbl)
	if hasNestedTable {
		return true
	}
	if hasHeader {
		return false
	}
	if maxCols <= 1 {
		return true
	}
	return attr(tbl, "border") == "0" || attr(tbl, "cellpadding") != "" || attr(tbl, "cellspacing") != ""
}

func hasVisibleContent(n *html.Node) bool {
	if n.Type == html.TextNode && strings.TrimSpace(n.Data) != "" {
		return true
	}
	if n.Type == html.ElementNode && n.DataAtom == atom.Img && strings.TrimSpace(attr(n, "alt")) != "" {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if hasVisibleContent(c) {
			return true
		}
	}
	return false
}

func unwrapTable(tbl *html.Node) {
	parent := tbl.Parent
	var rows []*html.Node
	var collectRows func(*html.Node)
	collectRows = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.DataAtom {
			case atom.Tr:
				rows = append(rows, c)
			case atom.Thead, atom.Tbody, atom.Tfoot:
				collectRows(c)
			}
		}
	}
	collectRows(tbl)

	for _, tr := range rows {
		block := &html.Node{Type: html.ElementNode, DataAtom: atom.P, Data: "p"}
		wroteCell := false
		for cell := tr.FirstChild; cell != nil; cell = cell.NextSibling {
			if cell.Type != html.ElementNode || (cell.DataAtom != atom.Td && cell.DataAtom != atom.Th) {
				continue
			}
			if !hasVisibleContent(cell) {
				continue
			}
			if wroteCell {
				block.AppendChild(&html.Node{Type: html.TextNode, Data: " "})
			}
			for child := cell.FirstChild; child != nil; {
				next := child.NextSibling
				cell.RemoveChild(child)
				block.AppendChild(child)
				child = next
			}
			wroteCell = true
		}
		if wroteCell {
			parent.InsertBefore(block, tbl)
		}
	}
	parent.RemoveChild(tbl)
}
