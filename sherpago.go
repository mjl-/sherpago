package sherpago

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"bitbucket.org/mjl/sherpa"
)

type sherpaType interface {
	GoType() string
}

// baseType can be one of: "any", "bool", "int", "float", "string".
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
	case "int":
		return "int64"
	case "float":
		return "float64"
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

func check(err error, action string) {
	if err != nil {
		log.Fatalf("%s: %s\n", action, err)
	}
}

// Main reads sherpadoc from stdin and writes a Go package to
// stdout.  It requires two parameters: the API name as exportable Go identifier, and a baseURL for the API.
// It is a separate command so it can easily be vendored into a repository with go modules.
func Main() {
	log.SetFlags(0)
	log.SetPrefix("sherpago: ")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s API-name baseURL\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(2)
	}
	flag.Parse()
	args := flag.Args()
	if len(args) != 2 {
		log.Print("bad parameters")
		flag.Usage()
		os.Exit(2)
	}
	apiName := args[0]
	baseURL := args[1]

	if apiName == "" || strings.ToUpper(apiName[:1]) != apiName[:1] {
		log.Fatalf("bad API name %q: must be an exported name in Go\n", apiName)
	}
	_, err := url.Parse(baseURL)
	check(err, "parsing base URL")
	if !strings.HasSuffix(baseURL, "/") {
		log.Fatalf("bad baseURL %q: must end with a slash", baseURL)
	}

	var doc sherpa.Doc
	err = json.NewDecoder(os.Stdin).Decode(&doc)
	check(err, "parsing sherpadoc json from stdin")

	const sherpadocVersion = 1
	if doc.Version != sherpadocVersion {
		log.Fatalf("unexpected sherpadoc version %d, expected %d\n", doc.Version, sherpadocVersion)
	}

	// Use bytes.Buffer, writes won't fail. We do one big write at the end. Output won't quickly become too big to fit in memory.
	out := &bytes.Buffer{}

	// Check all referenced types exist.
	checkTypes(&doc)

	// TODO: check that this name isn't already used as a type, field, function name in the same context.
	goExportedName := func(name string) string {
		return lintName(strings.ToUpper(name[:1]) + name[1:])
	}

	goLocalName := func(name string) string {
		return strings.ToLower(name[:1]) + name[1:]
	}

	fmt.Fprintf(out, `package client

import (
	"bytes"
	"encoding/json"
	"net/http"

	"bitbucket.org/mjl/sherpa"
)

type %s struct {
	BaseURL string
}

func New() *%s {
	return &%s{
		BaseURL: "%s",
	}
}

func (c *%s) call(functionName string, params []interface{}, result []interface{}) error {
	req := map[string]interface{}{
		"params": params,
	}
	buf := &bytes.Buffer{}
	err := json.NewEncoder(buf).Encode(req)
	if err != nil {
		return &sherpa.Error{Code: "sherpa:parameter encode error", Message: "could not encode request parameters: " + err.Error()}
	}
	url := c.BaseURL + functionName
	resp, err := http.Post(url, "application/json", buf)
	if err != nil {
		return &sherpa.Error{Code: sherpa.SherpaHTTPError, Message: "sending POST request: " + err.Error()}
	}
	switch resp.StatusCode {
	case 200:
		defer resp.Body.Close()
		var response struct {
			Result json.RawMessage "json:\"result\""
			Error  *sherpa.Error   "json:\"error\""
		}
		err = json.NewDecoder(resp.Body).Decode(&response)
		if err != nil {
			return &sherpa.Error{Code: sherpa.SherpaBadResponse, Message: "could not parse JSON response: " + err.Error()}
		}
		if response.Error != nil {
			return response.Error
		}
		var r interface{}
		if len(result) == 1 {
			r = result[0]
		}
		err = json.Unmarshal(response.Result, r)
		if err != nil {
			return &sherpa.Error{Code: sherpa.SherpaBadResponse, Message: "could not unmarshal JSON response"}
		}
		return nil
	case 404:
		return &sherpa.Error{Code: sherpa.SherpaBadFunction, Message: "no such function"}
	default:
		return &sherpa.Error{Code: sherpa.SherpaHTTPError, Message: "HTTP error from server: " + resp.Status}
	}
}

`, apiName, apiName, apiName, baseURL, apiName)

	generateTypes := func(sec *sherpa.Doc) {
		for _, t := range sec.Types {
			for _, line := range commentLines(t.Text) {
				fmt.Fprintf(out, "// %s\n", line)
			}
			fmt.Fprintf(out, "type %s struct {\n", goExportedName(t.Name))
			for _, f := range t.Fields {
				lines := commentLines(f.Text)
				if len(lines) > 1 {
					for _, line := range lines {
						fmt.Fprintf(out, "\t// %s\n", line)
					}
				}
				what := fmt.Sprintf("field %s for type %s", f.Name, t.Name)
				fmt.Fprintf(out, "\t%s %s `json:\"%s\"`", goExportedName(f.Name), goType(what, f.Type), f.Name)
				if len(lines) == 1 {
					fmt.Fprintf(out, "   // %s", lines[0])
				}
				fmt.Fprintln(out, "")
			}
			fmt.Fprintf(out, "}\n\n")
		}
	}

	generateFunctions := func(sec *sherpa.Doc) {
		for _, fn := range sec.Functions {
			whatParam := "pararameter for " + fn.Name
			paramTypes := []string{}
			paramNames := []string{}
			params := []string{}
			for _, p := range fn.Params {
				paramType := goType(whatParam, p.Type)
				paramName := goLocalName(p.Name)
				paramTypes = append(paramTypes, paramType)
				paramNames = append(paramNames, paramName)
				params = append(params, fmt.Sprintf("%s %s", paramName, paramType))
			}

			returnVars := ""
			returnTypes := ""
			returnNames := ""
			returnRefNames := []string{}
			for i, t := range fn.Return {
				typ := goType(whatParam, t.Type)
				name := fmt.Sprintf("r%d", i)
				returnVars += fmt.Sprintf("\t\t%s %s\n", name, typ)
				returnTypes += typ + ", "
				returnNames += name + ", "
				returnRefNames = append(returnRefNames, "&"+name)
			}
			if returnVars != "" {
				returnVars = "\tvar (\n" + returnVars + "\t)\n"
			}
			for _, line := range commentLines(fn.Text) {
				fmt.Fprintf(out, "// %s\n", line)
			}
			fmt.Fprintf(out, `func (c *%s) %s(%s) (%serror) {
%s	err := c.call("%s", []interface{}{%s}, []interface{}{%s})
	return %serr
}

`, apiName, goExportedName(fn.Name), strings.Join(params, ", "), returnTypes, returnVars, fn.Name, strings.Join(paramNames, ", "), strings.Join(returnRefNames, ", "), returnNames)
		}
	}

	var generateSection func(sec *sherpa.Doc)
	generateSection = func(sec *sherpa.Doc) {
		generateTypes(sec)
		generateFunctions(sec)
		for _, subsec := range sec.Sections {
			generateSection(subsec)
		}
	}
	generateSection(&doc)

	_, err = os.Stdout.Write(out.Bytes())
	check(err, "write to stdout")
}

func goType(what string, typeTokens []string) string {
	t := parseType(what, typeTokens)
	return t.GoType()
}

func parseType(what string, tokens []string) sherpaType {
	checkOK := func(ok bool, v interface{}, msg string) {
		if !ok {
			log.Fatalf("invalid type for %s: %s, saw %q\n", what, msg, v)
		}
	}
	checkOK(len(tokens) > 0, tokens, "need at least one element")
	s := tokens[0]
	tokens = tokens[1:]
	switch s {
	case "any", "bool", "int", "float", "string":
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

func commentLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
