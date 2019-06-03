package sherpago

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mjl-/sherpadoc"
)

type sherpaType interface {
	GoType() string
}

// baseType, one of: "any", "bool", "int16", etc.
type baseType struct {
	Name string
}

// nullableType is: "nullable" <type>.
type nullableType struct {
	Type sherpaType
}

// arrayType is: "[]" <type>
type arrayType struct {
	Type sherpaType
}

// objectType is: "{}" <type>
type objectType struct {
	Value sherpaType
}

// identType is: [a-zA-Z][a-zA-Z0-9]*
type identType struct {
	Name string
}

func (t baseType) GoType() string {
	switch t.Name {
	case "any":
		return "interface{}"
	case "timestamp":
		return "time.Time"
	case "int64s":
		return "int64"
	case "uint64s":
		return "uint64"
	default:
		return t.Name
	}
}

func (t nullableType) GoType() string {
	return "*" + t.Type.GoType()
}

func (t arrayType) GoType() string {
	return "[]" + t.Type.GoType()
}

func (t objectType) GoType() string {
	return fmt.Sprintf("map[string]%s", t.Value.GoType())
}

func (t identType) GoType() string {
	return t.Name
}

type genError struct{ error }

// Generate reads sherpadoc from in and writes a Go file containing a client
// package to out.  It requires two parameters: the package name to use and the
// baseURL for the API.
func Generate(in io.Reader, out io.Writer, packageName, baseURL string) (retErr error) {
	defer func() {
		e := recover()
		if e == nil {
			return
		}
		g, ok := e.(genError)
		if !ok {
			panic(e)
		}
		retErr = error(g)
	}()

	var doc sherpadoc.Section
	err := json.NewDecoder(in).Decode(&doc)
	if err != nil {
		panic(genError{fmt.Errorf("parsing sherpadoc json: %s", err)})
	}

	const sherpadocVersion = 1
	if doc.SherpadocVersion != sherpadocVersion {
		panic(genError{fmt.Errorf("unexpected sherpadoc version %d, expected %d", doc.SherpadocVersion, sherpadocVersion)})
	}

	// Validate contents.
	err = sherpadoc.Check(&doc)
	if err != nil {
		panic(genError{err})
	}

	goExportedName := func(name string) string {
		return lintName(strings.ToUpper(name[:1]) + name[1:])
	}

	goLocalName := func(name string) string {
		return strings.ToLower(name[:1]) + name[1:]
	}

	bout := bufio.NewWriter(out)
	xprintf := func(format string, args ...interface{}) {
		_, err := fmt.Fprintf(out, format, args...)
		if err != nil {
			panic(genError{err})
		}
	}

	xprintMultiline := func(indent, docs string, always bool) []string {
		lines := docLines(docs)
		if len(lines) == 1 && !always {
			return lines
		}
		for _, line := range lines {
			xprintf("%s// %s\n", indent, line)
		}
		return lines
	}

	xprintSingleline := func(lines []string) {
		if len(lines) != 1 {
			return
		}
		xprintf("  // %s", lines[0])
	}

	var generateSectionDocs func(sec *sherpadoc.Section, depth int)
	generateSectionDocs = func(sec *sherpadoc.Section, depth int) {
		xprintMultiline("", sec.Docs, true)
		depth++
		for _, subsec := range sec.Sections {
			xprintf("//\n// %s %s\n//\n", strings.Repeat("#", depth), subsec.Name)
			generateSectionDocs(subsec, depth)
		}
	}
	generateSectionDocs(&doc, 0)

	xprintf(`package %s

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mjl-/sherpa"
)

var _ time.Time // in case "timestamp" is used

type Client struct {
	BaseURL string
	Client *http.Client
}

func NewClient() *Client {
	return &Client{
		BaseURL: "%s",
		Client: http.DefaultClient,
	}
}

func (c *Client) call(ctx context.Context, functionName string, params []interface{}, result []interface{}) error {
	sherpaReq := map[string]interface{}{
		"params": params,
	}
	buf := &bytes.Buffer{}
	err := json.NewEncoder(buf).Encode(sherpaReq)
	if err != nil {
		return &sherpa.Error{Code: "sherpa:parameter encode error", Message: "encoding request parameters: " + err.Error()}
	}

	url := c.BaseURL + functionName
	req, err := http.NewRequest("POST", url, buf)
	if err != nil {
		return &sherpa.Error{Code: "sherpa:http", Message: "constructing request: " + err.Error()}
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.Client.Do(req)
	if err != nil {
		return &sherpa.Error{Code: sherpa.SherpaHTTPError, Message: "sending POST request: " + err.Error()}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		var response struct {
			Result json.RawMessage "json:\"result\""
			Error  *sherpa.Error   "json:\"error\""
		}
		err = json.NewDecoder(resp.Body).Decode(&response)
		if err != nil {
			return &sherpa.Error{Code: sherpa.SherpaBadResponse, Message: "parsing response: " + err.Error()}
		}
		if response.Error != nil {
			return response.Error
		}

		var r interface{} = &result
		if len(result) == 1 {
			r = &result[0]
		}
		err = json.Unmarshal(response.Result, r)
		if err != nil {
			return &sherpa.Error{Code: sherpa.SherpaBadResponse, Message: "parsing result: " + err.Error()}
		}
		return nil
	case 404:
		return &sherpa.Error{Code: sherpa.SherpaBadFunction, Message: "no such function"}
	default:
		return &sherpa.Error{Code: sherpa.SherpaHTTPError, Message: "HTTP error from server: " + resp.Status}
	}
}

`, packageName, baseURL)

	generateTypes := func(sec *sherpadoc.Section) {
		for _, t := range sec.Structs {
			xprintMultiline("", t.Docs, true)
			xprintf("type %s struct {\n", goExportedName(t.Name))
			for _, f := range t.Fields {
				lines := xprintMultiline("\t", f.Docs, false)
				what := fmt.Sprintf("field %s for type %s", f.Name, t.Name)
				jsonStr := ""
				switch f.Typewords[len(f.Typewords)-1] {
				case "int64s", "uint64s":
					jsonStr = ",string"
				}
				goFieldName := goExportedName(f.Name)
				xprintf("\t%s %s", goFieldName, goType(what, f.Typewords))
				if goFieldName != f.Name || jsonStr != "" {
					xprintf(" `json:\"")
					if goFieldName != f.Name {
						xprintf("%s", f.Name)
					}
					xprintf("%s", jsonStr)
					xprintf("\"`")
				}
				xprintSingleline(lines)
				xprintf("\n")
			}
			xprintf("}\n\n")
		}

		for _, t := range sec.Ints {
			xprintMultiline("", t.Docs, true)
			typeName := goExportedName(t.Name)
			xprintf("type %s int\n", typeName)
			if len(t.Values) == 0 {
				continue
			}
			xprintf("const (\n")
			for _, v := range t.Values {
				lines := xprintMultiline("\t", v.Docs, false)
				xprintf("\t%s %s = %d", goExportedName(v.Name), typeName, v.Value)
				xprintSingleline(lines)
				xprintf("\n")
			}
			xprintf(")\n\n")
		}

		for _, t := range sec.Strings {
			xprintMultiline("", t.Docs, true)
			typeName := goExportedName(t.Name)
			xprintf("type %s string\n", typeName)
			if len(t.Values) == 0 {
				continue
			}
			xprintf("const (\n")
			for _, v := range t.Values {
				lines := xprintMultiline("\t", v.Docs, false)
				xprintf("\t%s %s = %s", goExportedName(v.Name), typeName, strconv.Quote(v.Value))
				xprintSingleline(lines)
				xprintf("\n")
			}
			xprintf(")\n\n")
		}
	}

	generateFunctions := func(sec *sherpadoc.Section) {
		for _, fn := range sec.Functions {
			whatParam := "pararameter for " + fn.Name
			paramTypes := []string{}
			paramNames := []string{}
			params := []string{}
			for _, p := range fn.Params {
				paramType := goType(whatParam, p.Typewords)
				paramName := goLocalName(p.Name)
				paramTypes = append(paramTypes, paramType)
				paramNames = append(paramNames, paramName)
				params = append(params, fmt.Sprintf("%s %s", paramName, paramType))
			}

			returnVars := ""
			returnTypes := ""
			returnNames := ""
			returnRefNames := []string{}
			for i, t := range fn.Returns {
				typ := goType(whatParam, t.Typewords)
				name := fmt.Sprintf("r%d", i)
				returnVars += fmt.Sprintf("\t\t%s %s\n", name, typ)
				returnTypes += typ + ", "
				returnNames += name + ", "
				returnRefNames = append(returnRefNames, "&"+name)
			}
			if returnVars != "" {
				returnVars = "\tvar (\n" + returnVars + "\t)\n"
			}
			xprintMultiline("", fn.Docs, true)
			xprintf(`func (c *Client) %s(ctx context.Context, %s) (%serror) {
%s	err := c.call(ctx, "%s", []interface{}{%s}, []interface{}{%s})
	return %serr
}

`, goExportedName(fn.Name), strings.Join(params, ", "), returnTypes, returnVars, fn.Name, strings.Join(paramNames, ", "), strings.Join(returnRefNames, ", "), returnNames)
		}
	}

	var generateSection func(sec *sherpadoc.Section)
	generateSection = func(sec *sherpadoc.Section) {
		generateTypes(sec)
		generateFunctions(sec)
		for _, subsec := range sec.Sections {
			generateSection(subsec)
		}
	}
	generateSection(&doc)

	err = bout.Flush()
	if err != nil {
		panic(genError{err})
	}
	return nil
}

func goType(what string, typeTokens []string) string {
	t := parseType(what, typeTokens)
	return t.GoType()
}

func parseType(what string, tokens []string) sherpaType {
	checkOK := func(ok bool, v interface{}, msg string) {
		if !ok {
			panic(genError{fmt.Errorf("invalid type for %s: %s, saw %q", what, msg, v)})
		}
	}
	checkOK(len(tokens) > 0, tokens, "need at least one element")
	s := tokens[0]
	tokens = tokens[1:]
	switch s {
	case "any", "bool", "int8", "uint8", "int16", "uint16", "int32", "uint32", "int64", "uint64", "int64s", "uint64s", "float32", "float64", "string", "timestamp":
		if len(tokens) != 0 {
			checkOK(false, tokens, "leftover tokens after base type")
		}
		return baseType{s}
	case "nullable":
		return nullableType{parseType(what, tokens)}
	case "[]":
		return arrayType{parseType(what, tokens)}
	case "{}":
		return objectType{parseType(what, tokens)}
	default:
		if len(tokens) != 0 {
			checkOK(false, tokens, "leftover tokens after identifier type")
		}
		return identType{s}
	}
}

func docLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
