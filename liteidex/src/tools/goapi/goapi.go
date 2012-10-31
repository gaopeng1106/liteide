// Copyright 2011 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Api computes the exported API of a set of Go packages.
//
// 2012.10.17 fixed for any package
// visualfc

package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Flags
var (
	checkFile  = flag.String("c", "", "optional filename to check API against")
	allowNew   = flag.Bool("allow_new", true, "allow API additions")
	exceptFile = flag.String("except", "", "optional filename of packages that are allowed to change without triggering a failure in the tool")
	nextFile   = flag.String("next", "", "optional filename of tentative upcoming API features for the next release. This file can be lazily maintained. It only affects the delta warnings from the -c file printed on success.")
	verbose    = flag.Bool("v", false, "verbose debugging")
	allmethods = flag.Bool("e", true, "extract for all embedded methods")
	alldecls   = flag.Bool("a", false, "extract for all declarations")
	showpos    = flag.Bool("pos", false, "addition token position")
	separate   = flag.String("sep", ", ", "setup separators")
	dep_parser = flag.Bool("dep", true, "parser package imports")
	defaultCtx = flag.Bool("default_ctx", false, "extract for default context")
	customCtx  = flag.String("custom_ctx", "", "optional comma-separated list of <goos>-<goarch>[-cgo] to override default contexts.")
	cursortype = flag.String("cursor_type", "", "print cursor node type \"file.go:pos\"")
)

var (
	cursor_file string
	cursor_pos  token.Pos = token.NoPos
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage: api [std|all|package...|local-dir]
       api std
       api -default_ctx=true fmt flag
       api -default_ctx=true -a ./cmd/go
`)
	flag.PrintDefaults()
}

// contexts are the default contexts which are scanned, unless
// overridden by the -contexts flag.
var contexts = []*build.Context{
	{GOOS: "linux", GOARCH: "386", CgoEnabled: true},
	{GOOS: "linux", GOARCH: "386"},
	{GOOS: "linux", GOARCH: "amd64", CgoEnabled: true},
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "arm"},
	{GOOS: "darwin", GOARCH: "386", CgoEnabled: true},
	{GOOS: "darwin", GOARCH: "386"},
	{GOOS: "darwin", GOARCH: "amd64", CgoEnabled: true},
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "windows", GOARCH: "amd64"},
	{GOOS: "windows", GOARCH: "386"},
	{GOOS: "freebsd", GOARCH: "amd64"},
	{GOOS: "freebsd", GOARCH: "386"},
}

func contextName(c *build.Context) string {
	s := c.GOOS + "-" + c.GOARCH
	if c.CgoEnabled {
		return s + "-cgo"
	}
	return s
}

func osArchName(c *build.Context) string {
	return c.GOOS + "-" + c.GOARCH
}

func parseContext(c string) *build.Context {
	parts := strings.Split(c, "-")
	if len(parts) < 2 {
		log.Fatalf("bad context: %q", c)
	}
	bc := &build.Context{
		GOOS:   parts[0],
		GOARCH: parts[1],
	}
	if len(parts) == 3 {
		if parts[2] == "cgo" {
			bc.CgoEnabled = true
		} else {
			log.Fatalf("bad context: %q", c)
		}
	}
	return bc
}

func setCustomContexts() {
	contexts = []*build.Context{}
	for _, c := range strings.Split(*customCtx, ",") {
		contexts = append(contexts, parseContext(c))
	}
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if !strings.Contains(runtime.Version(), "weekly") && !strings.Contains(runtime.Version(), "devel") {
		if *nextFile != "" {
			fmt.Printf("Go version is %q, ignoring -next %s\n", runtime.Version(), *nextFile)
			*nextFile = ""
		}
	}

	if flag.NArg() == 0 {
		flag.Usage()
		return
	}

	if *verbose {
		now := time.Now()
		defer func() {
			log.Println("time", time.Now().Sub(now))
		}()
	}

	var pkgs []string

	if flag.Arg(0) == "std" || flag.Arg(0) == "all" {
		out, err := exec.Command("go", "list", flag.Arg(0)).Output()
		if err != nil {
			log.Fatal(err)
		}
		pkgs = strings.Fields(string(out))
	} else {
		pkgs = flag.Args()
	}

	if *cursortype != "" {
		pos := strings.Index(*cursortype, ":")
		if pos != -1 {
			cursor_file = (*cursortype)[:pos]
			if i, err := strconv.Atoi((*cursortype)[pos+1:]); err == nil {
				cursor_pos = token.Pos(i)
			}
		}
	}
	var cursorPkg string
	if len(pkgs) == 1 && cursor_pos != token.NoPos {
		cursorPkg = pkgs[0]
	}

	if *customCtx != "" {
		*defaultCtx = false
		setCustomContexts()
	}

	var features []string

	if *defaultCtx {
		w := NewWalker()
		w.context = &build.Default
		w.cursorPkg = cursorPkg
		w.sep = *separate + " "

		for _, pkg := range pkgs {
			w.wantedPkg[pkg] = true
		}

		for _, pkg := range pkgs {
			w.WalkPackage(pkg)
		}
		features = w.Features("")
	} else {
		for _, c := range contexts {
			c.Compiler = build.Default.Compiler
		}

		w := NewWalker()
		for _, pkg := range pkgs {
			w.wantedPkg[pkg] = true
		}

		var featureCtx = make(map[string]map[string]bool) // feature -> context name -> true
		for _, context := range contexts {
			w.context = context
			w.ctxName = contextName(w.context) + ":"
			w.sep = *separate

			for _, pkg := range pkgs {
				w.WalkPackage(pkg)
			}
			//			ctxName := contextName(context)
			//			for _, f := range w.Features(ctxName) {
			//				if featureCtx[f] == nil {
			//					featureCtx[f] = make(map[string]bool)
			//				}
			//				featureCtx[f][ctxName] = true
			//			}
		}

		for pkg, p := range w.packageMap {
			if w.wantedPkg[p.name] {
				pos := strings.Index(pkg, ":")
				if pos == -1 {
					continue
				}
				ctxName := pkg[:pos]
				for _, f := range p.Features() {
					if featureCtx[f] == nil {
						featureCtx[f] = make(map[string]bool)
					}
					featureCtx[f][ctxName] = true
				}
			}
		}

		for f, cmap := range featureCtx {
			if len(cmap) == len(contexts) {
				features = append(features, f)
				continue
			}
			comma := strings.Index(f, ",")
			for cname := range cmap {
				f2 := fmt.Sprintf("%s (%s)%s", f[:comma], cname, f[comma:])
				features = append(features, f2)
			}
		}
		sort.Strings(features)
	}

	fail := false
	defer func() {
		if fail {
			os.Exit(1)
		}
	}()

	bw := bufio.NewWriter(os.Stdout)
	defer bw.Flush()

	if *checkFile == "" {
		for _, f := range features {
			fmt.Fprintf(bw, "%s\n", f)
		}
		return
	}

	var required []string
	for _, filename := range []string{*checkFile} {
		required = append(required, fileFeatures(filename)...)
	}
	sort.Strings(required)

	var optional = make(map[string]bool) // feature => true
	if *nextFile != "" {
		for _, feature := range fileFeatures(*nextFile) {
			optional[feature] = true
		}
	}

	var exception = make(map[string]bool) // exception => true
	if *exceptFile != "" {
		for _, feature := range fileFeatures(*exceptFile) {
			exception[feature] = true
		}
	}

	take := func(sl *[]string) string {
		s := (*sl)[0]
		*sl = (*sl)[1:]
		return s
	}

	for len(required) > 0 || len(features) > 0 {
		switch {
		case len(features) == 0 || required[0] < features[0]:
			feature := take(&required)
			if exception[feature] {
				fmt.Fprintf(bw, "~%s\n", feature)
			} else {
				fmt.Fprintf(bw, "-%s\n", feature)
				fail = true // broke compatibility
			}
		case len(required) == 0 || required[0] > features[0]:
			newFeature := take(&features)
			if optional[newFeature] {
				// Known added feature to the upcoming release.
				// Delete it from the map so we can detect any upcoming features
				// which were never seen.  (so we can clean up the nextFile)
				delete(optional, newFeature)
			} else {
				fmt.Fprintf(bw, "+%s\n", newFeature)
				if !*allowNew {
					fail = true // we're in lock-down mode for next release
				}
			}
		default:
			take(&required)
			take(&features)
		}
	}

	// In next file, but not in API.
	var missing []string
	for feature := range optional {
		missing = append(missing, feature)
	}
	sort.Strings(missing)
	for _, feature := range missing {
		fmt.Fprintf(bw, "±%s\n", feature)
	}
}

func fileFeatures(filename string) []string {
	bs, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatalf("Error reading file %s: %v", filename, err)
	}
	text := strings.TrimSpace(string(bs))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func isExtract(name string) bool {
	if *alldecls {
		return true
	}
	return ast.IsExported(name)
}

// pkgSymbol represents a symbol in a package
type pkgSymbol struct {
	pkg    string // "net/http"
	symbol string // "RoundTripper"
}

type constInfo struct {
	typ string
	pos token.Pos
}

type Package struct {
	dpkg             *doc.Package
	apkg             *ast.Package
	interfaceMethods map[string]([]method)
	interfaces       map[string]*ast.InterfaceType //interface
	structs          map[string]*ast.StructType    //struct
	types            map[string]ast.Expr           //type
	functions        map[string]method             //function 
	consts           map[string]string             //const => type
	vars             map[string]string             //var => type
	name             string
	dir              string
	sep              string
	deps             []string
	features         map[string](token.Pos) // set	
}

func NewPackage() *Package {
	return &Package{
		interfaceMethods: make(map[string]([]method)),
		interfaces:       make(map[string]*ast.InterfaceType),
		structs:          make(map[string]*ast.StructType),
		types:            make(map[string]ast.Expr),
		functions:        make(map[string]method),
		consts:           make(map[string]string),
		vars:             make(map[string]string),
		features:         make(map[string](token.Pos)),
		sep:              ", ",
	}
}

func (p *Package) Features() (fs []string) {
	for f, ps := range p.features {
		if *showpos {
			fs = append(fs, f+p.sep+strconv.Itoa(int(ps)))
		} else {
			fs = append(fs, f)
		}
	}
	sort.Strings(fs)
	return
}

func (p *Package) findType(name string) ast.Expr {
	for k, v := range p.interfaces {
		if k == name {
			return v
		}
	}
	for k, v := range p.structs {
		if k == name {
			return v
		}
	}
	for k, v := range p.types {
		if k == name {
			return v
		}
	}
	return nil
}

func funcRetType(ft *ast.FuncType, index int) ast.Expr {
	if ft.Results != nil {
		pos := 0
		for _, fi := range ft.Results.List {
			if fi.Names == nil {
				if pos == index {
					return fi.Type
				}
				pos++
			} else {
				for _ = range fi.Names {
					if pos == index {
						return fi.Type
					}
					pos++
				}
			}
		}
	}
	return nil
}

func findFunction(funcs []*doc.Func, name string) *ast.FuncType {
	for _, f := range funcs {
		if f.Name == name {
			return f.Decl.Type
		}
	}
	return nil
}

func (p *Package) findSelectorType(name string) ast.Expr {
	if t, ok := p.vars[name]; ok {
		return &ast.Ident{
			Name: t,
		}
	}
	if t, ok := p.consts[name]; ok {
		return &ast.Ident{
			Name: t,
		}
	}
	if t, ok := p.functions[name]; ok {
		return t.ft
	}
	for k, v := range p.structs {
		if k == name {
			return &ast.Ident{
				NamePos: v.Pos(),
				Name:    name,
			}
		}
	}
	for k, v := range p.types {
		if k == name {
			return v
		}
	}
	return nil
}

func (p *Package) findCallType(name string, index int) ast.Expr {
	if fn, ok := p.functions[name]; ok {
		return funcRetType(fn.ft, index)
	}
	if s, ok := p.structs[name]; ok {
		return &ast.Ident{
			NamePos: s.Pos(),
			Name:    name,
		}
	}
	if t, ok := p.types[name]; ok {
		return &ast.Ident{
			NamePos: t.Pos(),
			Name:    name,
		}
	}
	return nil
}

func (p *Package) findMethod(typ, name string) *ast.FuncType {
	for k, v := range p.interfaceMethods {
		if k == typ {
			for _, m := range v {
				if m.name == name {
					return m.ft
				}
			}
		}
	}
	for _, t := range p.dpkg.Types {
		if t.Name == typ {
			return findFunction(t.Methods, name)
		}
	}
	return nil
}

type Walker struct {
	context *build.Context
	fset    *token.FileSet
	scope   []string
	//	features        map[string](token.Pos) // set
	lastConstType   string
	curPackageName  string
	sep             string
	ctxName         string
	curPackage      *Package
	constDep        map[string]*constInfo // key's const identifier has type of future value const identifier
	packageState    map[string]loadState
	packageMap      map[string]*Package
	interfaces      map[pkgSymbol]*ast.InterfaceType
	selectorFullPkg map[string]string // "http" => "net/http", updated by imports
	wantedPkg       map[string]bool   // packages requested on the command line
	cursorPkg       string
	localvar        map[string]string
}

func NewWalker() *Walker {
	return &Walker{
		fset: token.NewFileSet(),
		//		features:        make(map[string]token.Pos),
		packageState:    make(map[string]loadState),
		interfaces:      make(map[pkgSymbol]*ast.InterfaceType),
		packageMap:      make(map[string]*Package),
		selectorFullPkg: make(map[string]string),
		wantedPkg:       make(map[string]bool),
		localvar:        make(map[string]string),
		sep:             ", ",
	}
}

// loadState is the state of a package's parsing.
type loadState int

const (
	notLoaded loadState = iota
	loading
	loaded
)

func (w *Walker) Features(ctx string) (fs []string) {
	for pkg, p := range w.packageMap {
		if w.wantedPkg[p.name] {
			if ctx == "" || strings.HasPrefix(pkg, ctx) {
				fs = append(fs, p.Features()...)
			}
		}
	}
	sort.Strings(fs)
	return
}

// fileDeps returns the imports in a file.
func fileDeps(f *ast.File) (pkgs []string) {
	for _, is := range f.Imports {
		fpkg, err := strconv.Unquote(is.Path.Value)
		if err != nil {
			log.Fatalf("error unquoting import string %q: %v", is.Path.Value, err)
		}
		if fpkg != "C" {
			pkgs = append(pkgs, fpkg)
		}
	}
	return
}

func (w *Walker) findPackage(pkg string) *Package {
	if full, ok := w.selectorFullPkg[pkg]; ok {
		if w.ctxName != "" {
			ctxName := w.ctxName + full
			for k, v := range w.packageMap {
				if k == ctxName {
					return v
				}
			}
		}
		for k, v := range w.packageMap {
			if k == full {
				return v
			}
		}
	}
	return nil
}

func (w *Walker) findPackageOSArch(pkg string) *Package {
	if full, ok := w.selectorFullPkg[pkg]; ok {
		ctxName := osArchName(w.context) + ":" + full
		for k, v := range w.packageMap {
			if k == ctxName {
				return v
			}
		}
	}
	return nil
}

// WalkPackage walks all files in package `name'.
// WalkPackage does nothing if the package has already been loaded.

func (w *Walker) WalkPackage(pkg string) {
	if build.IsLocalImport(pkg) {
		wd, err := os.Getwd()
		if err != nil {
			if *verbose {
				log.Println(err)
			}
			return
		}
		dir := path.Clean(path.Join(wd, pkg))
		bp, err := w.context.ImportDir(dir, 0)
		if err != nil {
			if *verbose {
				log.Println(err)
			}
			return
		}
		if w.wantedPkg[pkg] == true {
			w.wantedPkg[bp.Name] = true
			delete(w.wantedPkg, pkg)
		}
		if w.cursorPkg == pkg {
			w.cursorPkg = bp.Name
		}
		w.WalkPackageDir(bp.Name, bp.Dir, bp)
	} else if filepath.IsAbs(pkg) {
		bp, err := build.ImportDir(pkg, 0)
		if err != nil {
			if *verbose {
				log.Println(err)
			}
		}
		if w.wantedPkg[pkg] == true {
			w.wantedPkg[bp.Name] = true
			delete(w.wantedPkg, pkg)
		}
		if w.cursorPkg == pkg {
			w.cursorPkg = bp.Name
		}
		w.WalkPackageDir(bp.Name, bp.Dir, bp)
	} else {
		bp, err := build.Import(pkg, "", build.FindOnly)
		if err != nil {
			if *verbose {
				log.Println(err)
			}
			return
		}
		w.WalkPackageDir(pkg, bp.Dir, nil)
	}
}

func (w *Walker) WalkPackageDir(name string, dir string, bp *build.Package) {
	ctxName := w.ctxName + name
	curName := name
	switch w.packageState[ctxName] {
	case loading:
		log.Fatalf("import cycle loading package %q?", name)
		return
	case loaded:
		return
	}
	w.packageState[ctxName] = loading
	w.selectorFullPkg[name] = name

	defer func() {
		w.packageState[ctxName] = loaded
	}()

	sname := name[strings.LastIndex(name, "/")+1:]

	apkg := &ast.Package{
		Files: make(map[string]*ast.File),
	}
	if bp == nil {
		bp, _ = w.context.ImportDir(dir, 0)
	}
	if bp == nil {
		return
	}
	if w.ctxName != "" {
		isCgo := (len(bp.CgoFiles) > 0) && w.context.CgoEnabled
		if isCgo {
			curName = ctxName
		} else {
			isOSArch := false
			for _, file := range bp.GoFiles {
				if isOSArchFile(w.context, file) {
					isOSArch = true
					break
				}
			}
			var p *Package
			if isOSArch {
				curName = osArchName(w.context) + ":" + name
				p = w.findPackageOSArch(name)
			} else {
				curName = name
				p = w.findPackage(name)
			}
			if p != nil {
				if *dep_parser {
					for _, dep := range p.deps {
						if _, ok := w.packageState[dep]; ok {
							continue
						}
						w.WalkPackage(dep)
					}
				}
				w.packageMap[ctxName] = p
				return
			}
		}
	}

	files := append(append([]string{}, bp.GoFiles...), bp.CgoFiles...)
	if len(files) == 0 {
		if *verbose {
			log.Println("no Go source files in", bp.Dir)
		}
		return
	}
	var deps []string
	for _, file := range files {
		f, err := parser.ParseFile(w.fset, filepath.Join(dir, file), nil, 0)
		if err != nil {
			if *verbose {
				log.Printf("error parsing package %s, file %s: %v", name, file, err)
			}
		}

		if sname != f.Name.Name {
			continue
		}
		apkg.Files[file] = f
		if *dep_parser {
			deps = fileDeps(f)
			for _, dep := range deps {
				if _, ok := w.packageState[dep]; ok {
					continue
				}
				w.WalkPackage(dep)
			}
		}
		if *showpos && w.wantedPkg[name] {
			tf := w.fset.File(f.Pos())
			if tf != nil {
				fmt.Printf("pos %s,%s,%d,%d\n", name, filepath.Join(dir, file), tf.Base(), tf.Size())
			}
		}
	}
	/* else {
		fdir, err := os.Open(dir)
		if err != nil {
			log.Fatalln(err)
		}
		infos, err := fdir.Readdir(-1)
		fdir.Close()
		if err != nil {
			log.Fatalln(err)
		}

		for _, info := range infos {
			if info.IsDir() {
				continue
			}
			file := info.Name()
			if strings.HasPrefix(file, "_") || strings.HasSuffix(file, "_test.go") {
				continue
			}
			if strings.HasSuffix(file, ".go") {
				f, err := parser.ParseFile(w.fset, filepath.Join(dir, file), nil, 0)
				if err != nil {
					if *verbose {
						log.Printf("error parsing package %s, file %s: %v", name, file, err)
					}
					continue
				}
				if f.Name.Name != sname {
					continue
				}

				apkg.Files[file] = f
				if *dep_parser {
					for _, dep := range fileDeps(f) {
						w.WalkPackage(dep)
					}
				}
				if *showpos && w.wantedPkg[name] {
					tf := w.fset.File(f.Pos())
					if tf != nil {
						fmt.Printf("pos %s%s%s%s%d:%d\n", name, w.sep, filepath.Join(dir, file), w.sep, tf.Base(), tf.Base()+tf.Size())
					}
				}
			}
		}
	}*/
	if curName != ctxName {
		w.packageState[curName] = loading

		defer func() {
			w.packageState[curName] = loaded
		}()
	}

	if *verbose {
		log.Printf("package %s => %s", ctxName, curName)
	}
	pop := w.pushScope("pkg " + name)
	defer pop()

	w.curPackageName = curName
	w.constDep = map[string]*constInfo{}
	w.curPackage = NewPackage()
	w.curPackage.apkg = apkg
	w.curPackage.name = name
	w.curPackage.dir = dir
	w.curPackage.deps = deps
	w.curPackage.sep = w.sep
	w.packageMap[curName] = w.curPackage
	w.packageMap[ctxName] = w.curPackage

	for _, afile := range apkg.Files {
		w.recordTypes(afile)
	}

	// Register all function declarations first.
	for _, afile := range apkg.Files {
		for _, di := range afile.Decls {
			if d, ok := di.(*ast.FuncDecl); ok {
				w.peekFuncDecl(d)
			}
		}
	}

	for _, afile := range apkg.Files {
		w.walkFile(afile)
	}

	w.resolveConstantDeps()

	if w.cursorPkg == name {
		for k, v := range apkg.Files {
			if k == cursor_file {
				node, err := w.lookupFile(v, v.Pos()+cursor_pos-1)
				if err != nil {
					log.Fatalln("lookup, err->", err)
				}
				fmt.Println("lookup->", node)
			}
		}
		return
	}

	// Now that we're done walking types, vars and consts
	// in the *ast.Package, use go/doc to do the rest
	// (functions and methods). This is done here because
	// go/doc is destructive.  We can't use the
	// *ast.Package after this.
	var mode doc.Mode
	if *allmethods {
		mode |= doc.AllMethods
	}
	if *alldecls {
		mode |= doc.AllDecls
	}

	dpkg := doc.New(apkg, name, mode)
	w.curPackage.dpkg = dpkg

	if w.wantedPkg[name] != true {
		return
	}

	for _, t := range dpkg.Types {
		// Move funcs up to the top-level, not hiding in the Types.
		dpkg.Funcs = append(dpkg.Funcs, t.Funcs...)

		for _, m := range t.Methods {
			w.walkFuncDecl(m.Decl)
		}
	}

	for _, f := range dpkg.Funcs {
		w.walkFuncDecl(f.Decl)
	}
}

// pushScope enters a new scope (walking a package, type, node, etc)
// and returns a function that will leave the scope (with sanity checking
// for mismatched pushes & pops)
func (w *Walker) pushScope(name string) (popFunc func()) {
	w.scope = append(w.scope, name)
	return func() {
		if len(w.scope) == 0 {
			log.Fatalf("attempt to leave scope %q with empty scope list", name)
		}
		if w.scope[len(w.scope)-1] != name {
			log.Fatalf("attempt to leave scope %q, but scope is currently %#v", name, w.scope)
		}
		w.scope = w.scope[:len(w.scope)-1]
	}
}

func (w *Walker) recordTypes(file *ast.File) {
	cur := w.curPackage
	for _, di := range file.Decls {
		switch d := di.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, sp := range d.Specs {
					ts := sp.(*ast.TypeSpec)
					name := ts.Name.Name
					switch t := ts.Type.(type) {
					case *ast.InterfaceType:
						if isExtract(name) {
							w.noteInterface(name, t)
						}
						cur.interfaces[name] = t
					case *ast.StructType:
						cur.structs[name] = t
					default:
						cur.types[name] = ts.Type
					}
				}
			}
		}
	}
}

func inRange(node ast.Node, p token.Pos) bool {
	if node == nil {
		return false
	}
	return p >= node.Pos() && p < node.End()
}

func (w *Walker) lookupFile(file *ast.File, p token.Pos) (string, error) {
	for _, di := range file.Decls {
		switch d := di.(type) {
		case *ast.GenDecl:
			if inRange(d, p) {
				return w.findCursorDecl(d, p)
			}
		case *ast.FuncDecl:
			if inRange(d.Name, p) {
				return w.findCursorDecl(d, p)
			}
			if d.Body != nil && inRange(d.Body, p) {
				return w.lookupStmt(d.Body, p)
			}
		default:
			return "", fmt.Errorf("un parser decl %T", di)
		}
	}
	return "", fmt.Errorf("un find cursor %v", w.fset.Position(p))
}

func (w *Walker) lookupStmt(vi ast.Stmt, p token.Pos) (string, error) {
	if vi == nil {
		return "", nil
	}
	log.Printf("lookup stem %v %T", vi, vi)
	switch v := vi.(type) {
	case *ast.BadStmt:
		//
	case *ast.EmptyStmt:
		//
	case *ast.LabeledStmt:
		//
	case *ast.DeclStmt:
		return w.lookupDecl(v.Decl, p)
	case *ast.AssignStmt:
		if len(v.Lhs) != len(v.Rhs) {
			return "", fmt.Errorf("lsh %d != rsh %d", len(v.Lhs), len(v.Rhs))
		}
		for i := 0; i < len(v.Lhs); i++ {
			if inRange(v.Lhs[i], p) {
				if v.Tok == token.DEFINE {
					return "", nil
				}
				return w.lookupExpr(v.Lhs[i], p)
			} else if inRange(v.Rhs[i], p) {
				return w.lookupExpr(v.Rhs[i], p)
			}
			if fl, ok := v.Rhs[i].(*ast.FuncLit); ok {
				if inRange(fl, p) {
					return w.lookupStmt(fl.Body, p)
				}
			}
			switch lt := v.Lhs[i].(type) {
			case *ast.Ident:
				typ, err := w.varValueType(v.Rhs[i], i)
				if err == nil {
					w.localvar[lt.Name] = typ
				}
			}
		}
	case *ast.ExprStmt:
		return w.lookupExpr(v.X, p)
	case *ast.BlockStmt:
		for _, st := range v.List {
			if inRange(st, p) {
				return w.lookupStmt(st, p)
			}
			_, err := w.lookupStmt(st, p)
			if err != nil {
				log.Println(err)
			}
		}
	case *ast.IfStmt:
		if inRange(v.Init, p) {
			return w.lookupStmt(v.Init, p)
		} else {
			w.lookupStmt(v.Init, p)
		}
		if inRange(v.Cond, p) {
			return w.lookupExpr(v.Cond, p)
		} else if inRange(v.Body, p) {
			return w.lookupStmt(v.Body, p)
		} else if inRange(v.Else, p) {
			return w.lookupStmt(v.Else, p)
		}
	case *ast.SendStmt:
		if inRange(v.Chan, p) {
			return w.lookupExpr(v.Chan, p)
		} else if inRange(v.Value, p) {
			return w.lookupExpr(v.Value, p)
		}
	case *ast.IncDecStmt:
		return w.lookupExpr(v.X, p)
	case *ast.GoStmt:
		return w.lookupExpr(v.Call, p)
	case *ast.DeferStmt:
		return w.lookupExpr(v.Call, p)
	case *ast.ReturnStmt:
		for _, r := range v.Results {
			if inRange(r, p) {
				return w.lookupExpr(r, p)
			}
		}
	case *ast.BranchStmt:
		//
	case *ast.CaseClause:
		for _, r := range v.List {
			if inRange(r, p) {
				return w.lookupExpr(r, p)
			}
		}
		for _, body := range v.Body {
			if inRange(body, p) {
				return w.lookupStmt(body, p)
			}
		}
	case *ast.SwitchStmt:
		if inRange(v.Init, p) {
			return w.lookupStmt(v.Init, p)
		} else {
			w.lookupStmt(v.Init, p)
		}
		if inRange(v.Tag, p) {
			return w.lookupExpr(v.Tag, p)
		} else if inRange(v.Body, p) {
			return w.lookupStmt(v.Body, p)
		}
	case *ast.TypeSwitchStmt:
		if inRange(v.Assign, p) {
			return w.lookupStmt(v.Assign, p)
		} else {
			w.lookupStmt(v.Assign, p)
		}
		if inRange(v.Init, p) {
			return w.lookupStmt(v.Init, p)
		} else {
			w.lookupStmt(v.Init, p)
		}
		if inRange(v.Body, p) {
			return w.lookupStmt(v.Body, p)
		}
	case *ast.CommClause:
		if inRange(v.Comm, p) {
			return w.lookupStmt(v.Comm, p)
		}
		for _, body := range v.Body {
			if inRange(body, p) {
				return w.lookupStmt(body, p)
			}
		}
	case *ast.SelectStmt:
		if inRange(v.Body, p) {
			return w.lookupStmt(v.Body, p)
		}
	case *ast.ForStmt:
		if inRange(v.Init, p) {
			return w.lookupStmt(v.Init, p)
		} else {
			w.lookupStmt(v.Init, p)
		}
		if inRange(v.Cond, p) {
			return w.lookupExpr(v.Cond, p)
		} else if inRange(v.Body, p) {
			return w.lookupStmt(v.Body, p)
		} else if inRange(v.Post, p) {
			return w.lookupStmt(v.Post, p)
		}
	case *ast.RangeStmt:
		if inRange(v.X, p) {
			return w.lookupStmt(v.Body, p)
		} else {
			if key, ok := v.Key.(*ast.Ident); ok {
				w.localvar[key.Name] = "int"
			}
			if value, ok := v.Value.(*ast.Ident); ok {
				typ, err := w.varValueType(v.X, 0)
				if err == nil {
					w.localvar[value.Name] = typ
				}
			}
		}
		if inRange(v.Body, p) {
			return w.lookupStmt(v.Body, p)
		}
	}
	return "", nil
}

func (w *Walker) lookupVar(vs *ast.ValueSpec, p token.Pos) (string, error) {
	if inRange(vs.Type, p) {
		return w.lookupExpr(vs.Type, p)
	}
	for n, ident := range vs.Names {
		typ := ""
		if vs.Type != nil {
			typ = w.nodeString(vs.Type)
		} else {
			if len(vs.Values) != 1 {
				if *verbose {
					log.Printf("error values in ValueSpec, var=%q,size=%d", ident.Name, len(vs.Values))
				}
				return "", nil
			}

			if inRange(vs.Values[0], p) {
				return w.lookupExpr(vs.Values[0], p)
			}

			var err error
			typ, err = w.varValueType(vs.Values[0], n)
			if err != nil {
				if *verbose {
					log.Printf("unknown type of variable %q, type %T, error = %v, pos=%s",
						ident.Name, vs.Values, err, w.fset.Position(vs.Pos()))
				}
				typ = "unknown-type"
			}
		}
		if inRange(ident, p) {
			return fmt.Sprintf("local:%s:%s", typ, ident.Name), nil
		}
		w.localvar[ident.Name] = typ
	}
	return "", nil
}

func (w *Walker) lookupDecl(di ast.Decl, p token.Pos) (string, error) {
	switch d := di.(type) {
	case *ast.GenDecl:
		switch d.Tok {
		case token.IMPORT:
		case token.CONST:
		case token.TYPE:
		case token.VAR:
			for _, sp := range d.Specs {
				if inRange(sp, p) {
					return w.lookupVar(sp.(*ast.ValueSpec), p)
				} else {
					w.lookupVar(sp.(*ast.ValueSpec), p)
				}
			}
			return "", nil
		default:
			log.Fatalf("unknown token type %d in GenDecl", d.Tok)
		}
	default:
		log.Printf("unhandled %T, %#v\n", di, di)
	}
	return "", fmt.Errorf("not lookupDecl %T", di)
}

func (w *Walker) lookupExpr(vi ast.Expr, p token.Pos) (string, error) {
	log.Printf("findExprNode %T %v", vi, w.nodeString(vi))
	switch v := vi.(type) {
	case *ast.BasicLit:
		litType, ok := varType[v.Kind]
		if !ok {
			return "", fmt.Errorf("unknown basic literal kind %#v", v)
		}
		return litType, nil
	case *ast.BinaryExpr:
		if inRange(v.X, p) {
			return w.lookupExpr(v.X, p)
		} else if inRange(v.Y, p) {
			return w.lookupExpr(v.Y, p)
		}
		return "", nil
	case *ast.CallExpr:
		for _, arg := range v.Args {
			if inRange(arg, p) {
				return w.lookupExpr(arg, p)
			}
		}
		switch ft := v.Fun.(type) {
		case *ast.Ident:
			if typ, ok := w.localvar[ft.Name]; ok {
				return fmt.Sprintf("local:%s:%s", typ, ft.Name), nil
			}
			if typ, ok := w.curPackage.functions[ft.Name]; ok {
				return fmt.Sprintf("global:%s:%s", typ, ft.Name), nil
			}
		case *ast.SelectorExpr:
			switch st := ft.X.(type) {
			case *ast.Ident:
				s, err := w.lookupExpr(st, p)
				if err != nil {
					return "", err
				}
				return s + "." + ft.Sel.Name, nil
			default:
				return "", fmt.Errorf("not find select %v %T", v, st)
			}
			//			typ, err := w.varValueType(ft.X, 0)
			//			if err == nil {
			//				if strings.HasPrefix(typ, "*") {
			//					typ = typ[1:]
			//				}
			//			}
			//			log.Println(typ, err)
			//			switch st := ft.X.(type) {
			//			case *ast.Ident:
			//				return w.varFunctionType(st.Name, ft.Sel.Name, index)
			//			case *ast.CallExpr:
			//				return w.varValueType(st, index)
			//			case *ast.SelectorExpr:
			//				typ, err := w.varValueType(st, index)
			//				if err == nil {
			//					return w.varFunctionType(typ, ft.Sel.Name, index)
			//				}
			//			case *ast.IndexExpr:
			//				typ, err := w.varValueType(st.X, index)
			//				if err == nil {
			//					if strings.HasPrefix(typ, "[]") {
			//						return w.varFunctionType(typ[2:], ft.Sel.Name, index)
			//					}
			//				}
			//			}
		}
		return "", fmt.Errorf("not find call %v %T", w.nodeString(v), v.Fun)
	case *ast.SelectorExpr:
		s, err := w.lookupExpr(v.X, p)
		if err != nil {
			return "", err
		}
		return s + "." + v.Sel.Name, nil
	case *ast.Ident:
		if typ, ok := w.localvar[v.Name]; ok {
			return fmt.Sprintf("local:%s:%s", typ, v.Name), nil
		}
		if pkg, ok := w.selectorFullPkg[v.Name]; ok {
			return fmt.Sprintf("pkg:%s:%s", pkg, v.Name), nil
		}
		if typ, ok := w.curPackage.vars[v.Name]; ok {
			return fmt.Sprintf("global:%s:%s", typ, v.Name), nil
		}
		if isBuiltinType(v.Name) {
			return v.Name, nil
		}
		return v.Name, nil
	default:
		log.Fatalf("=> %T %v", v, w.nodeString(v))
	}
	return "", fmt.Errorf("not lookupExpr %v %T", w.nodeString(vi), vi)
}

func (w *Walker) findCursorDecl(decl ast.Decl, p token.Pos) (string, error) {
	log.Printf("=> %v %T %v", decl, decl, p)
	return "", nil
	switch d := decl.(type) {
	case *ast.GenDecl:
		switch d.Tok {
		case token.IMPORT:
			//			for _, sp := range d.Specs {
			//				is := sp.(*ast.ImportSpec)
			//				fpath, err := strconv.Unquote(is.Path.Value)
			//				if err != nil {
			//					log.Fatal(err)
			//				}
			//				name := path.Base(fpath)
			//				if is.Name != nil {
			//					name = is.Name.Name
			//				}
			//				w.selectorFullPkg[name] = fpath
			//			}
		case token.CONST:
			//			for _, sp := range d.Specs {
			//				w.walkConst(sp.(*ast.ValueSpec))
			//			}
		case token.TYPE:
			//			for _, sp := range d.Specs {
			//				w.walkTypeSpec(sp.(*ast.TypeSpec))
			//			}
		case token.VAR:
			//			for _, sp := range d.Specs {
			//				w.walkVar(sp.(*ast.ValueSpec))
			//			}
		default:
			log.Fatalf("unknown token type %d in GenDecl", d.Tok)
		}
	case *ast.FuncDecl:
		log.Println(">>", d.Name.Name, p, d.Name.Pos(), d.Name.End())
		if p >= d.Body.Pos() && p < d.Body.End() {
			//log.Println(d.Body.List)
		}
		// Ignore. Handled in subsequent pass, by go/doc.
	default:
		log.Printf("unhandled %T, %#v\n", decl, decl)
	}
	return "", nil
}

func (w *Walker) walkFile(file *ast.File) {
	// Not entering a scope here; file boundaries aren't interesting.
	for _, di := range file.Decls {
		switch d := di.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.IMPORT:
				for _, sp := range d.Specs {
					is := sp.(*ast.ImportSpec)
					fpath, err := strconv.Unquote(is.Path.Value)
					if err != nil {
						log.Fatal(err)
					}
					name := path.Base(fpath)
					if is.Name != nil {
						name = is.Name.Name
					}
					w.selectorFullPkg[name] = fpath
				}
			case token.CONST:
				for _, sp := range d.Specs {
					w.walkConst(sp.(*ast.ValueSpec))
				}
			case token.TYPE:
				for _, sp := range d.Specs {
					w.walkTypeSpec(sp.(*ast.TypeSpec))
				}
			case token.VAR:
				for _, sp := range d.Specs {
					w.walkVar(sp.(*ast.ValueSpec))
				}
			default:
				log.Fatalf("unknown token type %d in GenDecl", d.Tok)
			}
		case *ast.FuncDecl:
			// Ignore. Handled in subsequent pass, by go/doc.
		default:
			log.Printf("unhandled %T, %#v\n", di, di)
			printer.Fprint(os.Stderr, w.fset, di)
			os.Stderr.Write([]byte("\n"))
		}
	}
}

var constType = map[token.Token]string{
	token.INT:    "ideal-int",
	token.FLOAT:  "ideal-float",
	token.STRING: "ideal-string",
	token.CHAR:   "ideal-char",
	token.IMAG:   "ideal-imag",
}

var varType = map[token.Token]string{
	token.INT:    "int",
	token.FLOAT:  "float64",
	token.STRING: "string",
	token.CHAR:   "rune",
	token.IMAG:   "complex128",
}

var builtinTypes = []string{
	"bool", "byte", "complex64", "complex128", "error", "float32", "float64",
	"int", "int8", "int16", "int32", "int64", "rune", "string",
	"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
}

func isBuiltinType(typ string) bool {
	for _, v := range builtinTypes {
		if v == typ {
			return true
		}
	}
	return false
}

func constTypePriority(typ string) int {
	switch typ {
	case "complex128":
		return 100
	case "ideal-imag":
		return 99
	case "complex64":
		return 98
	case "float64":
		return 97
	case "ideal-float":
		return 96
	case "float32":
		return 95
	case "int64":
		return 94
	case "int", "uint", "uintptr":
		return 93
	case "ideal-int":
		return 92
	case "int16", "uint16", "int8", "uint8", "byte":
		return 91
	case "ideal-char":
		return 90
	}
	return 101
}

func (w *Walker) constRealType(typ string) string {
	pos := strings.Index(typ, ".")
	if pos >= 0 {
		pkg := typ[:pos]
		if pkg == "C" {
			return "int"
		}
		typ = typ[pos+1:]
		if p := w.findPackage(pkg); p != nil {
			ret := p.findType(typ)
			if ret != nil {
				return w.nodeString(w.namelessType(ret))
			}
		}
	} else {
		ret := w.curPackage.findType(typ)
		if ret != nil {
			return w.nodeString(w.namelessType(ret))
		}
	}
	return typ
}

func (w *Walker) constValueType(vi interface{}) (string, error) {
	switch v := vi.(type) {
	case *ast.BasicLit:
		litType, ok := constType[v.Kind]
		if !ok {
			return "", fmt.Errorf("unknown basic literal kind %#v", v)
		}
		return litType, nil
	case *ast.UnaryExpr:
		return w.constValueType(v.X)
	case *ast.SelectorExpr:
		lhs := w.nodeString(v.X)
		rhs := w.nodeString(v.Sel)
		//fix_code example C.PROT_NONE
		if lhs == "C" {
			return lhs + "." + rhs, nil
		}
		if p := w.findPackage(lhs); p != nil {
			if ret, ok := p.consts[rhs]; ok {
				return pkgRetType(p.name, ret), nil
			}
		}
		return "", fmt.Errorf("unknown constant reference to %s.%s", lhs, rhs)
	case *ast.Ident:
		if v.Name == "iota" {
			return "ideal-int", nil // hack.
		}
		if v.Name == "false" || v.Name == "true" {
			return "bool", nil
		}
		if t, ok := w.curPackage.consts[v.Name]; ok {
			return t, nil
		}
		return constDepPrefix + v.Name, nil
	case *ast.BinaryExpr:
		//== > < ! != >= <=
		if v.Op == token.EQL || v.Op == token.LSS || v.Op == token.GTR || v.Op == token.NOT ||
			v.Op == token.NEQ || v.Op == token.LEQ || v.Op == token.GEQ {
			return "bool", nil
		}
		left, err := w.constValueType(v.X)
		if err != nil {
			return "", err
		}
		if v.Op == token.SHL || v.Op == token.SHR {
			return left, err
		}
		right, err := w.constValueType(v.Y)
		if err != nil {
			return "", err
		}
		//const left != right , one or two is ideal-
		if left != right {
			if strings.HasPrefix(left, constDepPrefix) && strings.HasPrefix(right, constDepPrefix) {
				// Just pick one.
				// e.g. text/scanner GoTokens const-dependency:ScanIdents, const-dependency:ScanFloats
				return left, nil
			}
			lp := constTypePriority(w.constRealType(left))
			rp := constTypePriority(w.constRealType(right))
			if lp >= rp {
				return left, nil
			} else {
				return right, nil
			}
			return "", fmt.Errorf("in BinaryExpr, unhandled type mismatch; left=%q, right=%q", left, right)
		}
		return left, nil
	case *ast.CallExpr:
		// Not a call, but a type conversion.
		typ := w.nodeString(v.Fun)
		switch typ {
		case "complex":
			return "complex128", nil
		case "real", "imag":
			return "float64", nil
		}
		return typ, nil
	case *ast.ParenExpr:
		return w.constValueType(v.X)
	}
	return "", fmt.Errorf("unknown const value type %T", vi)
}

func pkgRetType(pkg, ret string) string {
	pkg = pkg[strings.LastIndex(pkg, "/")+1:]
	start := strings.HasPrefix(ret, "*")
	var name = ret
	if start {
		name = ret[1:]
	}
	if ast.IsExported(name) {
		if start {
			return "*" + pkg + "." + name
		}
		return pkg + "." + name
	}
	return ret
}

func (w *Walker) findStructFieldType(st ast.Expr, name string) ast.Expr {
	if s, ok := st.(*ast.StructType); ok {
		for _, fi := range s.Fields.List {
			for _, n := range fi.Names {
				if n.Name == name {
					return fi.Type
				}
			}
		}
	}
	return nil
}

func (w *Walker) varFunctionType(name, sel string, index int) (string, error) {
	pos := strings.Index(name, ".")
	if pos != -1 {
		pkg := name[:pos]
		typ := name[pos+1:]

		if p := w.findPackage(pkg); p != nil {
			fn := p.findMethod(typ, sel)
			if fn != nil {
				ret := funcRetType(fn, index)
				if ret != nil {
					return pkgRetType(p.name, w.nodeString(w.namelessType(ret))), nil
				}
			}
		}
		return "", fmt.Errorf("unknown pkg type function pkg: %s.%s.%s", pkg, typ, sel)
	}
	//find local var.func()
	if ns, nt, n := w.resolveName(name); n >= 0 {
		var vt string
		if nt != nil {
			vt = w.nodeString(w.namelessType(nt))
		} else if ns != nil {
			typ, err := w.varValueType(ns, n)
			if err == nil {
				vt = typ
			}
		} else {
			typ := w.curPackage.findSelectorType(name)
			if typ != nil {
				vt = w.nodeString(w.namelessType(typ))
			}
		}
		if strings.HasPrefix(vt, "*") {
			vt = vt[1:]
		}
		if vt == "error" && sel == "Error" {
			return "string", nil
		}
		if fn, ok := w.curPackage.functions[vt+"."+sel]; ok {
			return w.nodeString(w.namelessType(funcRetType(fn.ft, index))), nil
		}
	}
	//find pkg.func()

	if p := w.findPackage(name); p != nil {
		typ := p.findCallType(sel, index)
		if typ != nil {
			return pkgRetType(p.name, w.nodeString(w.namelessType(typ))), nil
		}
		return "", fmt.Errorf("not find pkg func %v.%v", p.name, sel)
	}
	return "", fmt.Errorf("not find func %v.%v", name, sel)

}

func (w *Walker) varSelectorType(name string, sel string) (string, error) {
	pos := strings.Index(name, ".")
	if pos != -1 {
		pkg := name[:pos]
		typ := name[pos+1:]
		if p := w.findPackage(pkg); p != nil {
			t := p.findType(typ)
			if t != nil {
				typ := w.findStructFieldType(t, sel)
				if typ != nil {
					return w.nodeString(w.namelessType(typ)), nil
				}
			}
		}
		return "", fmt.Errorf("unknown pkg type selector pkg: %s.%s.%s", pkg, typ, sel)
	}
	vs, vt, n := w.resolveName(name)
	if n >= 0 {
		var typ string
		if vt != nil {
			typ = w.nodeString(w.namelessType(vt))
		} else {
			typ, _ = w.varValueType(vs, n)
		}
		if strings.HasPrefix(typ, "*") {
			typ = typ[1:]
		}
		//typ is type, find real type
		for k, v := range w.curPackage.types {
			if k == typ {
				typ = w.nodeString(w.namelessType(v))
			}
		}
		pos := strings.Index(typ, ".")
		if pos == -1 {
			t := w.curPackage.findType(typ)
			if t != nil {
				typ := w.findStructFieldType(t, sel)
				if typ != nil {
					return w.nodeString(w.namelessType(typ)), nil
				}
			}
		} else {
			name := typ[:pos]
			typ = typ[pos+1:]
			if p := w.findPackage(name); p != nil {
				t := p.findType(typ)
				if t != nil {
					typ := w.findStructFieldType(t, sel)
					if typ != nil {
						return w.nodeString(w.namelessType(typ)), nil
					}
				}
			}
		}
	}
	if p := w.findPackage(name); p != nil {
		typ := p.findSelectorType(sel)
		if typ != nil {
			return pkgRetType(p.name, w.nodeString(w.namelessType(typ))), nil
		}
	}
	return "", fmt.Errorf("unknown selector expr ident: %s.%s", name, sel)
}

func (w *Walker) varValueType(vi interface{}, index int) (string, error) {
	switch v := vi.(type) {
	case *ast.BasicLit:
		litType, ok := varType[v.Kind]
		if !ok {
			return "", fmt.Errorf("unknown basic literal kind %#v", v)
		}
		return litType, nil
	case *ast.CompositeLit:
		return w.nodeString(v.Type), nil
	case *ast.FuncLit:
		return w.nodeString(w.namelessType(v.Type)), nil
	case *ast.UnaryExpr:
		if v.Op == token.AND {
			typ, err := w.varValueType(v.X, index)
			return "*" + typ, err
		}
		return "", fmt.Errorf("unknown unary expr: %#v", v)
	case *ast.SelectorExpr:
		switch st := v.X.(type) {
		case *ast.Ident:
			return w.varSelectorType(st.Name, v.Sel.Name)
		case *ast.CallExpr:
			typ, err := w.varValueType(v.X, index)
			if err == nil {
				if strings.HasPrefix(typ, "*") {
					typ = typ[1:]
				}
				t := w.curPackage.findType(typ)
				if st, ok := t.(*ast.StructType); ok {
					for _, fi := range st.Fields.List {
						for _, n := range fi.Names {
							if n.Name == v.Sel.Name {
								return w.varValueType(fi.Type, index)
							}
						}
					}
				}
			}
		case *ast.SelectorExpr:
			typ, err := w.varValueType(v.X, index)
			if err == nil {
				return w.varSelectorType(typ, v.Sel.Name)
			}
		case *ast.IndexExpr:
			//log.Fatalln(st.X)
			typ, err := w.varValueType(st.X, index)
			//log.Fatalln(typ, err)
			if err == nil {
				if strings.HasPrefix(typ, "[]") {
					return w.varSelectorType(typ[2:], v.Sel.Name)
				}
			}
		}
		return "", fmt.Errorf("unknown selector expr: %T %s.%s", v.X, w.nodeString(v.X), v.Sel)
	case *ast.Ident:
		if v.Name == "true" || v.Name == "false" {
			return "bool", nil
		}
		if isBuiltinType(v.Name) {
			return v.Name, nil
		}
		vt := w.curPackage.findType(v.Name)
		if vt != nil {
			return w.nodeString(vt), nil
		}
		vs, _, n := w.resolveName(v.Name)
		if n >= 0 {
			return w.varValueType(vs, n)
		}
		return "", fmt.Errorf("unresolved identifier: %q", v.Name)
	case *ast.BinaryExpr:
		//== > < ! != >= <=
		if v.Op == token.EQL || v.Op == token.LSS || v.Op == token.GTR || v.Op == token.NOT ||
			v.Op == token.NEQ || v.Op == token.LEQ || v.Op == token.GEQ {
			return "bool", nil
		}
		left, err := w.varValueType(v.X, index)
		if err != nil {
			return "", err
		}
		right, err := w.varValueType(v.Y, index)
		if err != nil {
			return "", err
		}
		if left != right {
			return "", fmt.Errorf("in BinaryExpr, unhandled type mismatch; left=%q, right=%q", left, right)
		}
		return left, nil
	case *ast.ParenExpr:
		return w.varValueType(v.X, index)
	case *ast.CallExpr:
		switch ft := v.Fun.(type) {
		case *ast.ArrayType:
			return w.nodeString(v.Fun), nil
		case *ast.Ident:
			switch ft.Name {
			case "make":
				return w.nodeString(w.namelessType(v.Args[0])), nil
			case "new":
				return "*" + w.nodeString(w.namelessType(v.Args[0])), nil
			case "append":
				return w.varValueType(v.Args[0], 0)
			case "recover":
				return "interface{}", nil
			case "len", "cap", "copy":
				return "int", nil
			case "complex":
				return "complex128", nil
			case "real":
				return "float64", nil
			case "imag":
				return "float64", nil
			}
			if isBuiltinType(ft.Name) {
				return ft.Name, nil
			}
			typ := w.curPackage.findCallType(ft.Name, index)
			if typ != nil {
				return w.nodeString(w.namelessType(typ)), nil
			}
			//if var is func() type
			vs, _, n := w.resolveName(ft.Name)
			if n >= 0 {
				if vs != nil {
					typ, err := w.varValueType(vs, n)
					if err == nil {
						if strings.HasPrefix(typ, "func(") {
							expr, err := parser.ParseExpr(typ + "{}")
							if err == nil {
								if fl, ok := expr.(*ast.FuncLit); ok {
									retType := funcRetType(fl.Type, index)
									if retType != nil {
										return w.nodeString(w.namelessType(retType)), nil
									}
								}
							}
						}
					}
				}
			}
			return "", fmt.Errorf("unknown funcion %s %s", w.curPackageName, ft.Name)
		case *ast.SelectorExpr:
			typ, err := w.varValueType(ft.X, index)
			if err == nil {
				if strings.HasPrefix(typ, "*") {
					typ = typ[1:]
				}
				retType := w.curPackage.findCallType(typ+"."+ft.Sel.Name, index)
				if retType != nil {
					return w.nodeString(w.namelessType(retType)), nil
				}
			}
			switch st := ft.X.(type) {
			case *ast.Ident:
				return w.varFunctionType(st.Name, ft.Sel.Name, index)
			case *ast.CallExpr:
				return w.varValueType(st, index)
			case *ast.SelectorExpr:
				typ, err := w.varValueType(st, index)
				if err == nil {
					return w.varFunctionType(typ, ft.Sel.Name, index)
				}
			case *ast.IndexExpr:
				typ, err := w.varValueType(st.X, index)
				if err == nil {
					if strings.HasPrefix(typ, "[]") {
						return w.varFunctionType(typ[2:], ft.Sel.Name, index)
					}
				}
			}
		case *ast.FuncLit:
			retType := funcRetType(ft.Type, index)
			if retType != nil {
				return w.nodeString(w.namelessType(retType)), nil
			}
		case *ast.CallExpr:
			typ, err := w.varValueType(v.Fun, 0)
			if err == nil && strings.HasPrefix(typ, "func(") {
				expr, err := parser.ParseExpr(typ + "{}")
				if err == nil {
					if fl, ok := expr.(*ast.FuncLit); ok {
						retType := funcRetType(fl.Type, index)
						if retType != nil {
							return w.nodeString(w.namelessType(retType)), nil
						}
					}
				}
			}
		}
		return "", fmt.Errorf("not a known function %T %v", v.Fun, w.nodeString(v.Fun))
	case *ast.MapType:
		return fmt.Sprintf("map[%s](%s)", w.nodeString(w.namelessType(v.Key)), w.nodeString(w.namelessType(v.Value))), nil
	case *ast.ArrayType:
		return fmt.Sprintf("[]%s", w.nodeString(w.namelessType(v.Elt))), nil
	case *ast.FuncType:
		return w.nodeString(w.namelessType(v)), nil
	case *ast.IndexExpr:
		typ, err := w.varValueType(v.X, index)
		if err == nil {
			if strings.HasPrefix(typ, "[]") {
				return typ[2:], nil
			}
		}
	case *ast.ChanType:
		typ, err := w.varValueType(v.Value, index)
		if err == nil {
			if v.Dir == ast.RECV {
				return "<-chan " + typ, nil
			} else if v.Dir == ast.SEND {
				return "chan<- " + typ, nil
			}
			return "chan " + typ, nil
		}
	default:
		return "", fmt.Errorf("unknown value type %T", vi)
	}
	panic("unreachable")
}

// resolveName finds a top-level node named name and returns the node
// v and its type t, if known.
func (w *Walker) resolveName(name string) (v interface{}, t interface{}, n int) {
	for _, file := range w.curPackage.apkg.Files {
		for _, di := range file.Decls {
			switch d := di.(type) {
			case *ast.GenDecl:
				switch d.Tok {
				case token.VAR:
					for _, sp := range d.Specs {
						vs := sp.(*ast.ValueSpec)
						for i, vname := range vs.Names {
							if vname.Name == name {
								if len(vs.Values) == 1 {
									return vs.Values[0], vs.Type, i
								}
								return nil, vs.Type, i
							}
						}
					}
				}
			}
		}
	}
	return nil, nil, -1
}

// constDepPrefix is a magic prefix that is used by constValueType
// and walkConst to signal that a type isn't known yet. These are
// resolved at the end of walking of a package's files.
const constDepPrefix = "const-dependency:"

func (w *Walker) walkConst(vs *ast.ValueSpec) {
	for _, ident := range vs.Names {
		litType := ""
		if vs.Type != nil {
			litType = w.nodeString(vs.Type)
		} else {
			litType = w.lastConstType
			if vs.Values != nil {
				if len(vs.Values) != 1 {
					log.Fatalf("const %q, values: %#v", ident.Name, vs.Values)
				}
				var err error
				litType, err = w.constValueType(vs.Values[0])
				if err != nil {
					if *verbose {
						log.Printf("unknown kind in const %q (%T): %v", ident.Name, vs.Values[0], err)
					}
					litType = "unknown-type"
				}
			}
		}
		if strings.HasPrefix(litType, constDepPrefix) {
			dep := litType[len(constDepPrefix):]
			w.constDep[ident.Name] = &constInfo{dep, ident.Pos()}
			continue
		}
		if litType == "" {
			if *verbose {
				log.Printf("unknown kind in const %q", ident.Name)
			}
			continue
		}
		w.lastConstType = litType

		w.curPackage.consts[ident.Name] = litType

		if isExtract(ident.Name) {
			w.emitFeature(fmt.Sprintf("const %s %s", ident, litType), ident.Pos())
		}
	}
}

func (w *Walker) resolveConstantDeps() {
	var findConstType func(string) string
	findConstType = func(ident string) string {
		if dep, ok := w.constDep[ident]; ok {
			return findConstType(dep.typ)
		}
		if t, ok := w.curPackage.consts[ident]; ok {
			return t
		}
		return ""
	}
	for ident, info := range w.constDep {
		if !isExtract(ident) {
			continue
		}
		t := findConstType(ident)
		if t == "" {
			if *verbose {
				log.Printf("failed to resolve constant %q", ident)
			}
			continue
		}
		w.curPackage.consts[ident] = t
		w.emitFeature(fmt.Sprintf("const %s %s", ident, t), info.pos)
	}
}

func (w *Walker) walkVar(vs *ast.ValueSpec) {
	for n, ident := range vs.Names {
		typ := ""
		if vs.Type != nil {
			typ = w.nodeString(vs.Type)
		} else {
			if len(vs.Values) != 1 {
				if *verbose {
					log.Printf("error values in ValueSpec, var=%q,size=%d", ident.Name, len(vs.Values))
				}
				return
			}
			var err error
			typ, err = w.varValueType(vs.Values[0], n)
			if err != nil {
				if *verbose {
					log.Printf("unknown type of variable %q, type %T, error = %v, pos=%s",
						ident.Name, vs.Values, err, w.fset.Position(vs.Pos()))
				}
				typ = "unknown-type"
			}
		}
		w.curPackage.vars[ident.Name] = typ
		if isExtract(ident.Name) {
			w.emitFeature(fmt.Sprintf("var %s %s", ident, typ), ident.Pos())
		}
	}
}

func (w *Walker) nodeString(node interface{}) string {
	if node == nil {
		return ""
	}
	var b bytes.Buffer
	printer.Fprint(&b, w.fset, node)
	return b.String()
}

func (w *Walker) nodeDebug(node interface{}) string {
	if node == nil {
		return ""
	}
	var b bytes.Buffer
	ast.Fprint(&b, w.fset, node, nil)
	return b.String()
}

func (w *Walker) noteInterface(name string, it *ast.InterfaceType) {
	w.interfaces[pkgSymbol{w.curPackageName, name}] = it
}

func (w *Walker) walkTypeSpec(ts *ast.TypeSpec) {
	name := ts.Name.Name
	if !isExtract(name) {
		return
	}
	switch t := ts.Type.(type) {
	case *ast.StructType:
		w.walkStructType(name, t)
	case *ast.InterfaceType:
		w.walkInterfaceType(name, t)
	default:
		w.emitFeature(fmt.Sprintf("type %s %s", name, w.nodeString(ts.Type)), t.Pos()-token.Pos(len(name)+1))
	}
}

func (w *Walker) walkStructType(name string, t *ast.StructType) {
	typeStruct := fmt.Sprintf("type %s struct", name)
	w.emitFeature(typeStruct, t.Pos()-token.Pos(len(name)+1))
	pop := w.pushScope(typeStruct)
	defer pop()
	for _, f := range t.Fields.List {
		typ := f.Type
		for _, name := range f.Names {
			if isExtract(name.Name) {
				w.emitFeature(fmt.Sprintf("%s %s", name, w.nodeString(w.namelessType(typ))), name.Pos())
			}
		}
		if f.Names == nil {
			switch v := typ.(type) {
			case *ast.Ident:
				if isExtract(v.Name) {
					w.emitFeature(fmt.Sprintf("embedded %s", v.Name), v.Pos())
				}
			case *ast.StarExpr:
				switch vv := v.X.(type) {
				case *ast.Ident:
					if isExtract(vv.Name) {
						w.emitFeature(fmt.Sprintf("embedded *%s", vv.Name), vv.Pos())
					}
				case *ast.SelectorExpr:
					w.emitFeature(fmt.Sprintf("embedded %s", w.nodeString(typ)), v.Pos())
				default:
					log.Fatalf("unable to handle embedded starexpr before %T", typ)
				}
			case *ast.SelectorExpr:
				w.emitFeature(fmt.Sprintf("embedded %s", w.nodeString(typ)), v.Pos())
			default:
				if *verbose {
					log.Printf("unable to handle embedded %T", typ)
				}
			}
		}
	}
}

// method is a method of an interface.
type method struct {
	name string // "Read"
	sig  string // "([]byte) (int, error)", from funcSigString
	ft   *ast.FuncType
	pos  token.Pos
	recv ast.Expr
}

// interfaceMethods returns the expanded list of exported methods for an interface.
// The boolean complete reports whether the list contains all methods (that is, the
// interface has no unexported methods).
// pkg is the complete package name ("net/http")
// iname is the interface name.
func (w *Walker) interfaceMethods(pkg, iname string) (methods []method, complete bool) {
	t, ok := w.interfaces[pkgSymbol{pkg, iname}]
	if !ok {
		if *verbose {
			log.Printf("failed to find interface %s.%s", pkg, iname)
		}
		return
	}

	complete = true
	for _, f := range t.Methods.List {
		typ := f.Type
		switch tv := typ.(type) {
		case *ast.FuncType:
			for _, mname := range f.Names {
				if isExtract(mname.Name) {
					ft := typ.(*ast.FuncType)
					methods = append(methods, method{
						name: mname.Name,
						sig:  w.funcSigString(ft),
						ft:   ft,
						pos:  f.Pos(),
					})
				} else {
					complete = false
				}
			}
		case *ast.Ident:
			embedded := typ.(*ast.Ident).Name
			if embedded == "error" {
				methods = append(methods, method{
					name: "Error",
					sig:  "() string",
					ft: &ast.FuncType{
						Params: nil,
						Results: &ast.FieldList{
							List: []*ast.Field{
								&ast.Field{
									Type: &ast.Ident{
										Name: "string",
									},
								},
							},
						},
					},
					pos: f.Pos(),
				})
				continue
			}
			if !isExtract(embedded) {
				log.Fatalf("unexported embedded interface %q in exported interface %s.%s; confused",
					embedded, pkg, iname)
			}
			m, c := w.interfaceMethods(pkg, embedded)
			methods = append(methods, m...)
			complete = complete && c
		case *ast.SelectorExpr:
			lhs := w.nodeString(tv.X)
			rhs := w.nodeString(tv.Sel)
			fpkg, ok := w.selectorFullPkg[lhs]
			if !ok {
				log.Fatalf("can't resolve selector %q in interface %s.%s", lhs, pkg, iname)
			}
			m, c := w.interfaceMethods(fpkg, rhs)
			methods = append(methods, m...)
			complete = complete && c
		default:
			log.Fatalf("unknown type %T in interface field", typ)
		}
	}
	return
}

func (w *Walker) walkInterfaceType(name string, t *ast.InterfaceType) {
	methNames := []string{}
	pop := w.pushScope("type " + name + " interface")
	methods, complete := w.interfaceMethods(w.curPackageName, name)
	w.packageMap[w.curPackageName].interfaceMethods[name] = methods
	for _, m := range methods {
		methNames = append(methNames, m.name)
		w.emitFeature(fmt.Sprintf("%s%s", m.name, m.sig), m.pos)
	}
	if !complete {
		// The method set has unexported methods, so all the
		// implementations are provided by the same package,
		// so the method set can be extended. Instead of recording
		// the full set of names (below), record only that there were
		// unexported methods. (If the interface shrinks, we will notice
		// because a method signature emitted during the last loop,
		// will disappear.)
		w.emitFeature("unexported methods", 0)
	}
	pop()

	if !complete {
		return
	}

	sort.Strings(methNames)
	if len(methNames) == 0 {
		w.emitFeature(fmt.Sprintf("type %s interface {}", name), t.Pos()-token.Pos(len(name)+1))
	} else {
		w.emitFeature(fmt.Sprintf("type %s interface { %s }", name, strings.Join(methNames, ", ")), t.Pos()-token.Pos(len(name)+1))
	}
}

func baseTypeName(x ast.Expr) (name string, imported bool) {
	switch t := x.(type) {
	case *ast.Ident:
		return t.Name, false
	case *ast.SelectorExpr:
		if _, ok := t.X.(*ast.Ident); ok {
			// only possible for qualified type names;
			// assume type is imported
			return t.Sel.Name, true
		}
	case *ast.StarExpr:
		return baseTypeName(t.X)
	}
	return
}

func (w *Walker) peekFuncDecl(f *ast.FuncDecl) {
	var fname = f.Name.Name
	var recv ast.Expr
	if f.Recv != nil {
		recvTypeName, imp := baseTypeName(f.Recv.List[0].Type)
		if imp {
			return
		}
		fname = recvTypeName + "." + f.Name.Name
		recv = f.Recv.List[0].Type
	}
	// Record return type for later use.
	if f.Type.Results != nil && len(f.Type.Results.List) >= 1 {
		w.curPackage.functions[fname] = method{
			name: fname,
			sig:  w.funcSigString(f.Type),
			ft:   f.Type,
			pos:  f.Pos(),
			recv: recv,
		}
	}
}

func (w *Walker) walkFuncDecl(f *ast.FuncDecl) {
	if !isExtract(f.Name.Name) {
		return
	}
	if f.Recv != nil {
		// Method.
		recvType := w.nodeString(f.Recv.List[0].Type)
		keep := isExtract(recvType) ||
			(strings.HasPrefix(recvType, "*") &&
				isExtract(recvType[1:]))
		if !keep {
			return
		}
		w.emitFeature(fmt.Sprintf("method (%s) %s%s", recvType, f.Name.Name, w.funcSigString(f.Type)), f.Name.Pos())
		return
	}
	// Else, a function
	w.emitFeature(fmt.Sprintf("func %s%s", f.Name.Name, w.funcSigString(f.Type)), f.Name.Pos())
}

func (w *Walker) funcSigString(ft *ast.FuncType) string {
	var b bytes.Buffer
	writeField := func(b *bytes.Buffer, f *ast.Field) {
		if n := len(f.Names); n > 1 {
			for i := 0; i < n; i++ {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(w.nodeString(w.namelessType(f.Type)))
			}
		} else {
			b.WriteString(w.nodeString(w.namelessType(f.Type)))
		}
	}
	b.WriteByte('(')
	if ft.Params != nil {
		for i, f := range ft.Params.List {
			if i > 0 {
				b.WriteString(", ")
			}
			writeField(&b, f)
		}
	}
	b.WriteByte(')')
	if ft.Results != nil {
		nr := 0
		for _, f := range ft.Results.List {
			if n := len(f.Names); n > 1 {
				nr += n
			} else {
				nr++
			}
		}
		if nr > 0 {
			b.WriteByte(' ')
			if nr > 1 {
				b.WriteByte('(')
			}
			for i, f := range ft.Results.List {
				if i > 0 {
					b.WriteString(", ")
				}
				writeField(&b, f)
			}
			if nr > 1 {
				b.WriteByte(')')
			}
		}
	}
	return b.String()
}

// namelessType returns a type node that lacks any variable names.
func (w *Walker) namelessType(t interface{}) interface{} {
	ft, ok := t.(*ast.FuncType)
	if !ok {
		return t
	}
	return &ast.FuncType{
		Params:  w.namelessFieldList(ft.Params),
		Results: w.namelessFieldList(ft.Results),
	}
}

// namelessFieldList returns a deep clone of fl, with the cloned fields
// lacking names.
func (w *Walker) namelessFieldList(fl *ast.FieldList) *ast.FieldList {
	fl2 := &ast.FieldList{}
	if fl != nil {
		for _, f := range fl.List {
			fl2.List = append(fl2.List, w.namelessField(f))
		}
	}
	return fl2
}

// namelessField clones f, but not preserving the names of fields.
// (comments and tags are also ignored)
func (w *Walker) namelessField(f *ast.Field) *ast.Field {
	return &ast.Field{
		Type: f.Type,
	}
}

func (w *Walker) emitFeature(feature string, pos token.Pos) {
	if !w.wantedPkg[w.curPackage.name] {
		return
	}
	more := strings.Index(feature, "\n")
	if more != -1 {
		if len(feature) <= 1024 {
			feature = strings.Replace(feature, "\n", " ", 1)
			feature = strings.Replace(feature, "\n", ";", -1)
			feature = strings.Replace(feature, "\t", " ", -1)
		} else {
			feature = feature[:more] + " ...more"
			if *verbose {
				log.Printf("feature contains newlines: %v, %s", feature, w.fset.Position(pos))
			}
		}
	}
	f := strings.Join(w.scope, w.sep) + w.sep + feature

	if _, dup := w.curPackage.features[f]; dup {
		return
	}
	w.curPackage.features[f] = pos
}

func strListContains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}
