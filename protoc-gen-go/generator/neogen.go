package generator

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/NeoclubTechnology/protobuf/proto"
	"github.com/NeoclubTechnology/protobuf/protoc-gen-go/descriptor"
	"github.com/NeoclubTechnology/protobuf/protoc-gen-go/generator/internal/remap"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log"

	plugin "github.com/NeoclubTechnology/protobuf/protoc-gen-go/plugin"
)

type TmplGen struct {
	Generator
	PName  string
	Render func(g *Generator, fd *FileDescriptor)
}

// supposed to generate.
func (g *TmplGen) generate(file *FileDescriptor) {
	g.file = file

	if g.Render != nil {
		g.Render(&g.Generator, file)
	}

	// Generate header and imports last, though they appear first in the output.
	rem := g.Buffer
	remAnno := g.annotations
	g.Buffer = new(bytes.Buffer)
	g.annotations = nil
	if !g.writeOutput {
		return
	}
	// Adjust the offsets for annotations displaced by the header and imports.
	for _, anno := range remAnno {
		*anno.Begin += int32(g.Len())
		*anno.End += int32(g.Len())
		g.annotations = append(g.annotations, anno)
	}
	g.Write(rem.Bytes())

	// Reformat generated code and patch annotation locations.
	fset := token.NewFileSet()
	original := g.Bytes()
	if g.annotateCode {
		// make a copy independent of g; we'll need it after Reset.
		original = append([]byte(nil), original...)
	}
	fileAST, err := parser.ParseFile(fset, "", original, parser.ParseComments)
	if err != nil {
		// Print out the bad code with line numbers.
		// This should never happen in practice, but it can while changing generated code,
		// so consider this a debugging aid.
		var src bytes.Buffer
		s := bufio.NewScanner(bytes.NewReader(original))
		for line := 1; s.Scan(); line++ {
			fmt.Fprintf(&src, "%5d\t%s\n", line, s.Bytes())
		}
		g.Fail("bad Go source code was generated:", err.Error(), "\n"+src.String())
	}
	ast.SortImports(fset, fileAST)
	g.Reset()
	err = (&printer.Config{Mode: printer.TabIndent | printer.UseSpaces, Tabwidth: 8}).Fprint(g, fset, fileAST)
	if err != nil {
		g.Fail("generated Go source code could not be reformatted:", err.Error())
	}
	if g.annotateCode {
		m, err := remap.Compute(original, g.Bytes())
		if err != nil {
			g.Fail("formatted generated Go source code could not be mapped back to the original code:", err.Error())
		}
		for _, anno := range g.annotations {
			new, ok := m.Find(int(*anno.Begin), int(*anno.End))
			if !ok {
				g.Fail("span in formatted generated Go source code could not be mapped back to the original code")
			}
			*anno.Begin = int32(new.Pos)
			*anno.End = int32(new.End)
		}
	}
}

// GenerateAllFiles generates the output for all the files we're outputting.
func (g *TmplGen) GenerateAllFiles() {
	// Initialize the plugins
	for _, p := range plugins {
		p.Init(&g.Generator)
	}
	// Generate the output. The generator runs for every file, even the files
	// that we don't generate output for, so that we can collate the full list
	// of exported symbols to support public imports.
	genFileMap := make(map[*FileDescriptor]bool, len(g.genFiles))
	for _, file := range g.genFiles {
		genFileMap[file] = true
	}
	for _, file := range g.allFiles {
		g.Reset()
		g.annotations = nil
		g.writeOutput = genFileMap[file]
		g.generate(file)
		if !g.writeOutput {
			continue
		}
		fname := file.goPFileName(g.pathType, g.PName)
		log.Println(fname)
		g.Response.File = append(g.Response.File, &plugin.CodeGeneratorResponse_File{
			Name:    proto.String(fname),
			Content: proto.String(g.String()),
		})
		if g.annotateCode {
			// Store the generated code annotations in text, as the protoc plugin protocol requires that
			// strings contain valid UTF-8.
			g.Response.File = append(g.Response.File, &plugin.CodeGeneratorResponse_File{
				Name:    proto.String(file.goFileName(g.pathType) + ".meta"),
				Content: proto.String(proto.CompactTextString(&descriptor.GeneratedCodeInfo{Annotation: g.annotations})),
			})
		}
	}
}
