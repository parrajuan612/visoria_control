package docx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Un árbol XML mínimo que conserva el documento tal cual (prefijos, orden de atributos y
// espacios). Se usa RawToken para que encoding/xml NO reescriba los prefijos de espacios de
// nombres (w:, r:, mc:…), que es lo que rompe los .docx al reserializar con el árbol por defecto.

type nodeKind int

const (
	kElement nodeKind = iota
	kText             // texto (ya sin escapar)
	kComment
	kProcInst
	kDirective
)

type attr struct{ Name, Value string } // Name con su prefijo: "w:val", "xml:space", "xmlns:w"

type node struct {
	Kind     nodeKind
	Name     string // "w:p" (solo elementos)
	Attrs    []attr
	Children []*node
	Text     string // texto, comentario, instrucción o directiva
	parent   *node
}

func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

// parseXML lee un documento completo. Devuelve un nodo raíz sintético (Kind=kElement, Name="")
// que contiene la declaración <?xml?> y el elemento documento.
func parseXML(data []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	root := &node{Kind: kElement}
	cur := root
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML inválido: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{Kind: kElement, Name: qname(t.Name), parent: cur}
			for _, a := range t.Attr {
				n.Attrs = append(n.Attrs, attr{qname(a.Name), a.Value})
			}
			cur.Children = append(cur.Children, n)
			cur = n
		case xml.EndElement:
			if cur.parent == nil {
				return nil, fmt.Errorf("XML inválido: cierre inesperado </%s>", qname(t.Name))
			}
			cur = cur.parent
		case xml.CharData:
			cur.Children = append(cur.Children, &node{Kind: kText, Text: string(t), parent: cur})
		case xml.Comment:
			cur.Children = append(cur.Children, &node{Kind: kComment, Text: string(t), parent: cur})
		case xml.ProcInst:
			cur.Children = append(cur.Children, &node{Kind: kProcInst, Name: t.Target, Text: string(t.Inst), parent: cur})
		case xml.Directive:
			cur.Children = append(cur.Children, &node{Kind: kDirective, Text: string(t), parent: cur})
		}
	}
	if cur != root {
		return nil, fmt.Errorf("XML inválido: elementos sin cerrar")
	}
	return root, nil
}

var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#xA;", "\r", "&#xD;", "\t", "&#x9;")

func (n *node) write(b *bytes.Buffer) {
	switch n.Kind {
	case kText:
		b.WriteString(textEscaper.Replace(n.Text))
	case kComment:
		b.WriteString("<!--" + n.Text + "-->")
	case kProcInst:
		b.WriteString("<?" + n.Name)
		if n.Text != "" {
			b.WriteString(" " + n.Text)
		}
		b.WriteString("?>")
	case kDirective:
		b.WriteString("<!" + n.Text + ">")
	case kElement:
		if n.Name == "" { // raíz sintética
			for _, c := range n.Children {
				c.write(b)
			}
			return
		}
		b.WriteString("<" + n.Name)
		for _, a := range n.Attrs {
			b.WriteString(" " + a.Name + `="` + attrEscaper.Replace(a.Value) + `"`)
		}
		if len(n.Children) == 0 {
			b.WriteString("/>")
			return
		}
		b.WriteString(">")
		for _, c := range n.Children {
			c.write(b)
		}
		b.WriteString("</" + n.Name + ">")
	}
}

func (n *node) bytes() []byte {
	var b bytes.Buffer
	n.write(&b)
	return b.Bytes()
}

// ---- consultas ----

// el devuelve los hijos elemento con ese nombre.
func (n *node) els(name string) []*node {
	var out []*node
	for _, c := range n.Children {
		if c.Kind == kElement && c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func (n *node) first(name string) *node {
	for _, c := range n.Children {
		if c.Kind == kElement && c.Name == name {
			return c
		}
	}
	return nil
}

// path sigue una ruta de hijos: path("w:body", "w:sectPr").
func (n *node) path(names ...string) *node {
	cur := n
	for _, nm := range names {
		if cur = cur.first(nm); cur == nil {
			return nil
		}
	}
	return cur
}

func (n *node) attr(name string) (string, bool) {
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

func (n *node) setAttr(name, value string) {
	for i := range n.Attrs {
		if n.Attrs[i].Name == name {
			n.Attrs[i].Value = value
			return
		}
	}
	n.Attrs = append(n.Attrs, attr{name, value})
}

func (n *node) index(child *node) int {
	for i, c := range n.Children {
		if c == child {
			return i
		}
	}
	return -1
}

func (n *node) insertAt(i int, child *node) {
	child.parent = n
	n.Children = append(n.Children, nil)
	copy(n.Children[i+1:], n.Children[i:])
	n.Children[i] = child
}

func (n *node) remove(child *node) {
	if i := n.index(child); i >= 0 {
		n.Children = append(n.Children[:i], n.Children[i+1:]...)
		child.parent = nil
	}
}

func newEl(name string, attrs ...attr) *node { return &node{Kind: kElement, Name: name, Attrs: attrs} }
