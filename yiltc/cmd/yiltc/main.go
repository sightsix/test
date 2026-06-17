package main

import (
        "flag"
        "fmt"
        "io"
        "math"
        "os"
        "os/exec"
        "path/filepath"
        "runtime"
        "strconv"
        "sort"
        "strings"
        "sync"
        "syscall"
        "time"

        "github.com/yilt/yiltc/internal/ast"
        "github.com/yilt/yiltc/internal/check"
        yiltruntime "github.com/yilt/yiltc/internal/runtime"
        "github.com/yilt/yiltc/internal/codegen/aarch64"
        "github.com/yilt/yiltc/internal/codegen/x86_64"
        "github.com/yilt/yiltc/internal/diag"
        "github.com/yilt/yiltc/internal/ir"
        "github.com/yilt/yiltc/internal/lex"
        "github.com/yilt/yiltc/internal/link"
        "github.com/yilt/yiltc/internal/link/types"
        "github.com/yilt/yiltc/internal/ownership"
        "github.com/yilt/yiltc/internal/parse"
        "github.com/yilt/yiltc/internal/target"
)

const version = "1.0.0"

// stringDataMu protects stringDataMap and stringDataSeq during concurrent codegen.
var stringDataMu sync.Mutex

// stringDataMap collects string literal data during code generation.
// Keys are linker section names (e.g. ".yilt.str.0"), values are the string content.
var stringDataMap map[string]string

// stringDataSeq is a monotonically increasing counter for generating unique
// string data symbol names during concurrent code generation.
var stringDataSeq int

// CLI color codes (prefixed to avoid collision with diag package).
const (
        cliColorReset  = "\033[0m"
        cliColorBold   = "\033[1m"
        cliColorDim    = "\033[2m"
        cliColorGreen  = "\033[32m"
        cliColorCyan   = "\033[36m"
        cliColorYellow = "\033[33m"
        cliColorRed    = "\033[31m"
)

// pendingReloc records a cross-function call that needs a linker relocation.
type pendingReloc struct {
        offset uint64 // offset within the code section of the 4-byte displacement
        target string  // symbol name of the target function
}

// funcResult holds compiled code and its pending linker relocations.
type funcResult struct {
        name         string
        code         []byte
        relocations []pendingReloc
}

func main() {
        // Reorder args so flags appear before positional arguments.
        // Go's flag package stops parsing at the first non-flag arg,
        // but users expect flags anywhere:  yiltc file.yilt -o out -v
        os.Exit(run(os.Args[1:]))
}

// reorderFlags moves all flag arguments (and their values) before
// positional arguments.  It knows which flags take values so that
// flag values aren't accidentally treated as flags themselves.
func reorderFlags(args []string) []string {
        // Set of flags that consume the next argument as their value.
        valuedFlags := map[string]bool{
                "-o": true, "-output": true, "--output": true,
                "-t": true, "-target": true, "--target": true,
                "-O": true, "-optimize": true, "--optimize": true,
                "-j": true, "-jobs": true, "--jobs": true,
                "-W": true,
                "-e": true, "-eval": true, "--eval": true,
        }

        var flags, positional []string
        for i := 0; i < len(args); i++ {
                a := args[i]
                if len(a) > 0 && a[0] == '-' && !isNumber(a) {
                        flags = append(flags, a)
                        // If this flag takes a value, consume it too.
                        if valuedFlags[a] && i+1 < len(args) {
                                i++
                                flags = append(flags, args[i])
                        }
                } else {
                        positional = append(positional, a)
                }
        }
        return append(flags, positional...)
}

func isNumber(s string) bool {
        if len(s) == 0 {
                return false
        }
        for _, c := range s[1:] { // skip leading '-'
                if c < '0' || c > '9' {
                        return false
                }
        }
        return true
}

func run(args []string) int {
        reordered := reorderFlags(args)
        fs := flag.NewFlagSet("yiltc", flag.ContinueOnError)
        fs.Usage = func() { printUsage(fs) }

        var (
                output       string
                targetStr    string
                optLevel     int
                verbose      bool
                jobs         int
                emitIR       bool
                emitObj      bool
                emitAST      bool
                checkOnly    bool
                runAfter     bool
                evalExpr     string
                showVersion  bool
                listTargets  bool
                warnControl  string
                werror       bool
                quiet        bool
        )

        fs.StringVar(&output, "o", "", "output file path")
        fs.StringVar(&output, "output", "", "output file path")
        fs.StringVar(&targetStr, "t", "", "target triple (default: host)")
        fs.StringVar(&targetStr, "target", "", "target triple (default: host)")
        fs.IntVar(&optLevel, "O", 1, "optimization level (0=none, 1=basic, 2=more)")
        fs.IntVar(&optLevel, "optimize", 1, "optimization level")
        fs.BoolVar(&verbose, "v", false, "verbose output")
        fs.BoolVar(&verbose, "verbose", false, "verbose output")
        fs.IntVar(&jobs, "j", runtime.NumCPU(), "concurrent compilation jobs")
        fs.IntVar(&jobs, "jobs", runtime.NumCPU(), "concurrent compilation jobs")
        fs.BoolVar(&emitIR, "emit-ir", false, "emit IR to stdout")
        fs.BoolVar(&emitAST, "emit-ast", false, "emit AST to stderr")
        fs.BoolVar(&emitObj, "emit-obj", false, "emit object file only")
        fs.BoolVar(&checkOnly, "check", false, "type-check only (no codegen or linking)")
        fs.BoolVar(&runAfter, "run", false, "compile and run")
        fs.StringVar(&evalExpr, "e", "", "evaluate an expression")
        fs.StringVar(&evalExpr, "eval", "", "evaluate an expression")
        fs.BoolVar(&showVersion, "version", false, "print version")
        fs.BoolVar(&listTargets, "list-targets", false, "list all supported targets")
        fs.StringVar(&warnControl, "W", "all", "warning control: all, none, or comma-separated codes")
        fs.BoolVar(&werror, "Werror", false, "treat warnings as errors")
        fs.BoolVar(&quiet, "quiet", false, "suppress success and progress messages")

        fs.Parse(reordered)

        if showVersion {
                isTTY := isTerminal(os.Stdout)
                if isTTY {
                        fmt.Printf("%sYilt Compiler%s %s\n", cliColorBold, cliColorReset, version)
                } else {
                        fmt.Printf("Yilt Compiler %s\n", version)
                }
                return 0
        }

        if listTargets {
                for _, t := range target.AllTargets() {
                        fmt.Printf("  %-30s %s\n", t.Triple, t.String())
                }
                return 0
        }

        // --eval mode: no input file needed
        if evalExpr != "" {
                return runEval(evalExpr, output, targetStr, optLevel, verbose, jobs, warnControl, werror)
        }

        if fs.NArg() == 0 {
                if isTerminal(os.Stderr) {
                        fmt.Fprintf(os.Stderr, "%serror:%s no input file specified\n", cliColorBold+cliColorRed, cliColorReset)
                        fmt.Fprintf(os.Stderr, "  %sFor help, run: %syiltc --help%s\n", cliColorDim, cliColorCyan, cliColorReset)
                } else {
                        fmt.Fprintf(os.Stderr, "error: no input file specified\n")
                        fmt.Fprintf(os.Stderr, "  For help, run: yiltc --help\n")
                }
                return 1
        }

        input := fs.Arg(0)
        if _, err := os.Stat(input); os.IsNotExist(err) {
                isTTY := isTerminal(os.Stderr)
                if isTTY {
                        fmt.Fprintf(os.Stderr, "%serror:%s file not found: %s\n", cliColorBold+cliColorRed, cliColorReset, input)
                } else {
                        fmt.Fprintf(os.Stderr, "error: file not found: %s\n", input)
                }
                suggestFiles(input)
                return 1
        }

        // Resolve target
        var tgt target.Target
        if targetStr != "" {
                var err error
                tgt, err = target.ParseTarget(targetStr)
                if err != nil {
                        fmt.Fprintf(os.Stderr, "error: %s\n", err)
                        return 1
                }
        } else {
                tgt = target.DefaultTarget()
        }

        if err := target.ValidateTarget(tgt); err != nil {
                fmt.Fprintf(os.Stderr, "error: invalid target: %s\n", err)
                return 1
        }

        // Resolve output path
        if output == "" {
                base := filepath.Base(input)
                ext := filepath.Ext(base)
                name := strings.TrimSuffix(base, ext)
                switch tgt.Format {
                case "elf64", "pe64", "macho64":
                        if tgt.OS == "windows" {
                                output = name + ".exe"
                        } else {
                                output = name
                        }
                case "wasm":
                        output = name + ".wasm"
                default:
                        output = name + ".out"
                }
        }

        // Initialize diagnostics and terminal detection early (needed by verbose header)
        isTTY := isTerminal(os.Stderr)
        warnNone := warnControl == "none"

        if verbose {
                hdr := "yiltc " + version
                if isTTY {
                        hdr = cliColorBold + cliColorCyan + hdr + cliColorReset
                }
                fmt.Fprintf(os.Stderr, "%s\n", hdr)
                fmt.Fprintf(os.Stderr, "  input:  %s\n", input)
                fmt.Fprintf(os.Stderr, "  output: %s\n", output)
                fmt.Fprintf(os.Stderr, "  target: %s\n", tgt.Triple)
                fmt.Fprintf(os.Stderr, "  format: %s\n", tgt.Format)
                fmt.Fprintf(os.Stderr, "  jobs:   %d\n", jobs)
        }

        dh := diag.NewHandler(isTTY)
        compileStart := time.Now()

        // Read input source
        src, err := os.ReadFile(input)
        if err != nil {
                fmt.Fprintf(os.Stderr, "error: cannot read %s: %s\n", input, err)
                return 1
        }
        dh.AddSource(input, src)

        // Reset string data collection for this compilation.
        stringDataMap = make(map[string]string)
        stringDataSeq = 0

        // Phase 1: Lex
        phaseStart := time.Now()
        lexer := lex.New(input, src)
        lexer.SetDiag(dh)
        tokens, indents := lexer.Tokenize()
        if dh.HasErrors() {
                dh.Render()
                printAbortFooter(isTTY, dh)
                return 1
        }
        if verbose {
                phaseLine(isTTY, "Lexing", time.Since(phaseStart), fmt.Sprintf("%d tokens, %d indent events", len(tokens), len(indents)))
        }

        // Phase 2: Parse
        phaseStart = time.Now()
        parser := parse.New(input, tokens, indents, src)
        file := parser.ParseFile()
        if len(parser.Errors()) > 0 {
                for _, pe := range parser.Errors() {
                        if pe.Help != "" {
                                if pe.SpanLen > 1 {
                                        dh.ErrorfSpan(pe.File, pe.Line, pe.Col, pe.Offset, pe.SpanLen, "%s", pe.Msg)
                                } else {
                                        dh.Errorf(pe.File, pe.Line, pe.Col, pe.Offset, "%s", pe.Msg)
                                }
                                dh.Help(pe.Help)
                        } else if pe.SpanLen > 1 {
                                dh.ErrorSpan(pe.File, pe.Line, pe.Col, pe.Offset, pe.SpanLen, pe.Msg)
                        } else {
                                dh.Error(pe.File, pe.Line, pe.Col, pe.Offset, pe.Msg)
                        }
                }
        }
        if dh.HasErrors() {
                dh.Render()
                printAbortFooter(isTTY, dh)
                return 1
        }
        if verbose {
                phaseLine(isTTY, "Parsing", time.Since(phaseStart), fmt.Sprintf("%d top-level declarations", len(file.Decls)))
        }

        // Phase 2.5: Discover and parse local module dependencies.
        // Scans UseDecl nodes in all parsed files to find local (non-stdlib)
        // imports, loads and parses the referenced .yilt files, and repeats
        // until no new dependencies are found (BFS with cycle detection).
        allFiles := []*ast.File{file}
        visitedMods := map[string]bool{filepath.Base(input): true}
        queue := []string{filepath.Base(input)}
        rootDir := filepath.Dir(input)
        for len(queue) > 0 {
                modName := queue[0]
                queue = queue[1:]
                var modFile *ast.File
                for _, f := range allFiles {
                        if filepath.Base(f.Path) == modName || filepath.Base(f.Path) == modName+".yilt" {
                                modFile = f
                                break
                        }
                }
                if modFile == nil {
                        continue
                }
                for _, d := range modFile.Decls {
                        ud, ok := d.(*ast.UseDecl)
                        if !ok || ud.Module == "" {
                                continue
                        }
                        if lex.IsStdModule(ud.Module) || strings.HasPrefix(ud.Module, "ffi:") {
                                continue
                        }
                        for _, ext := range []string{".yilt", ".ylt", ""} {
                                candidate := ud.Module + ext
                                if visitedMods[candidate] {
                                        continue
                                }
                                absPath := filepath.Join(rootDir, candidate)
                                if _, err := os.Stat(absPath); err != nil {
                                        continue
                                }
                                depSrc, err := os.ReadFile(absPath)
                                if err != nil {
                                        fmt.Fprintf(os.Stderr, "error: cannot read dependency %s: %s\n", absPath, err)
                                        return 1
                                }
                                dh.AddSource(absPath, depSrc)
                                depLexer := lex.New(absPath, depSrc)
                                depLexer.SetDiag(dh)
                                depTokens, depIndents := depLexer.Tokenize()
                                if dh.HasErrors() {
                                        dh.Render()
                                        printAbortFooter(isTTY, dh)
                                        return 1
                                }
                                depParser := parse.New(absPath, depTokens, depIndents, depSrc)
                                depFile := depParser.ParseFile()
                                if len(depParser.Errors()) > 0 {
                                        for _, pe := range depParser.Errors() {
                                                if pe.Help != "" {
                                                        if pe.SpanLen > 1 {
                                                                dh.ErrorfSpan(pe.File, pe.Line, pe.Col, pe.Offset, pe.SpanLen, "%s", pe.Msg)
                                                        } else {
                                                                dh.Errorf(pe.File, pe.Line, pe.Col, pe.Offset, "%s", pe.Msg)
                                                        }
                                                        dh.Help(pe.Help)
                                                } else if pe.SpanLen > 1 {
                                                        dh.ErrorSpan(pe.File, pe.Line, pe.Col, pe.Offset, pe.SpanLen, pe.Msg)
                                                } else {
                                                        dh.Error(pe.File, pe.Line, pe.Col, pe.Offset, pe.Msg)
                                                }
                                        }
                                }
                                if dh.HasErrors() {
                                        dh.Render()
                                        printAbortFooter(isTTY, dh)
                                        return 1
                                }
                                allFiles = append(allFiles, depFile)
                                visitedMods[candidate] = true
                                queue = append(queue, candidate)
                                if verbose {
                                        phaseLine(isTTY, "Import", 0, fmt.Sprintf("loaded %s", candidate))
                                }
                                break
                        }
                }
        }
        if verbose && len(allFiles) > 1 {
                phaseLine(isTTY, "Dependencies", time.Since(phaseStart), fmt.Sprintf("%d files total", len(allFiles)))
        }

        // Phase 3: Type check
        phaseStart = time.Now()
        program := &ast.Program{
                Files: allFiles,
                Root:  rootDir,
        }
        checker := check.NewChecker(program, dh)
        checked, _ := checker.Check()
        if dh.HasErrors() {
                dh.Render()
                printAbortFooter(isTTY, dh)
                return 1
        }
        if verbose {
                phaseLine(isTTY, "Type check", time.Since(phaseStart), fmt.Sprintf("%d functions checked", len(checked.Functions)))
        }

        // Phase 3.1: Ownership analysis
        phaseStart = time.Now()
        ownAnalyzer := ownership.NewAnalyzer(checked, dh)
        ownAnalyzer.Analyze()
        if dh.HasErrors() {
                dh.Render()
                printAbortFooter(isTTY, dh)
                return 1
        }
        if verbose {
                phaseLine(isTTY, "Ownership", time.Since(phaseStart), "move semantics checked")
        }

        // Emit AST if requested (before any early returns below).
        if emitAST {
                printAST(file, dh)
        }

        // Phase 3.5: Check-only mode
        if checkOnly {
                if !warnNone && dh.WarningCount() > 0 {
                        dh.RenderWarnings()
                }
                if werror && dh.WarningCount() > 0 {
                        printAbortFooter(isTTY, dh)
                        return 1
                }
                if !quiet {
                        base := filepath.Base(input)
                        if isTTY {
                                fmt.Fprintf(os.Stderr, "%s%sChecked%s %s — %d error(s), %d warning(s)\n",
                                        cliColorBold, cliColorCyan, cliColorReset, base,
                                        dh.ErrorCount(), dh.WarningCount())
                        } else {
                                fmt.Fprintf(os.Stderr, "Checked %s — %d error(s), %d warning(s)\n",
                                        base, dh.ErrorCount(), dh.WarningCount())
                        }
                }
                return 0
        }

        // Phase 4: Generate IR via one-pass lowering
        phaseStart = time.Now()
        module := lowerToIR(checked)
        if emitIR {
                fmt.Println(module.String())
        }
        if verbose {
                phaseLine(isTTY, "IR gen", time.Since(phaseStart), fmt.Sprintf("%d IR functions generated", module.FuncCount()))
        }

        // Phase 5: Optimize (single pass, no extra passes)
        if optLevel > 0 {
                phaseStart = time.Now()
                applyOptimizations(module, optLevel)
                if verbose {
                        phaseLine(isTTY, "Optimizing", time.Since(phaseStart), fmt.Sprintf("O%d", optLevel))
                }
        }

        // Phase 5.5: Verify entry point exists
        if _, hasMain := module.Funcs["main"]; !hasMain {
                fmt.Fprintf(os.Stderr, "error: no 'main' function found — cannot produce executable\n")
                return 1
        }

        // Phase 6: Code generation and linking
        phaseStart = time.Now()

        // Concurrent code generation per function
        funcResults := make(chan funcResult, len(module.Funcs))
        var wg sync.WaitGroup
        sem := make(chan struct{}, jobs)

        for name, fn := range module.Funcs {
                wg.Add(1)
                sem <- struct{}{}
                go func(name string, fn *ir.Func) {
                        defer wg.Done()
                        defer func() { <-sem }()
                        funcResults <- generateCode(fn, tgt)
                }(name, fn)
        }
        go func() { wg.Wait(); close(funcResults) }()

        var allCode []funcResult
        for r := range funcResults {
                allCode = append(allCode, r)
        }

        // Link
        lnk := createLinker(tgt)
        if lnk == nil {
                return 1
        }
        for _, fr := range allCode {
                lnk.AddCode(fr.name, fr.code)
                // Add linker relocations for all cross-function calls emitted by the codegen.
                for _, rel := range fr.relocations {
                        lnk.AddRelocation(fr.name, rel.offset, rel.target, types.RelocPC32, -4)
                }
        }
        // Add collected string data to the linker as read-only data sections.
        // Each string is a separate section so the linker can resolve LEA
        // relocations from function code to string data.
        stringDataMu.Lock()
        for symName, strContent := range stringDataMap {
                data := make([]byte, len(strContent)+1)
                copy(data, strContent)
                data[len(strContent)] = 0 // null terminator
                lnk.AddData(symName, data)
        }
        stringDataMu.Unlock()

        // Generate _start entry stub and runtime support code.
        addStartAndRuntime(lnk, tgt)
        lnk.SetEntryPoint("_start")

        if emitObj {
                outFile, err := os.Create(output)
                if err != nil {
                        fmt.Fprintf(os.Stderr, "error: cannot create %s: %s\n", output, err)
                        return 1
                }
                for _, fr := range allCode {
                        outFile.Write(fr.code)
                }
                outFile.Close()
                if verbose {
                        phaseLine(isTTY, "Codegen", time.Since(phaseStart), fmt.Sprintf("%s + %s, %d functions", tgt.Arch, tgt.Format, len(allCode)))
                }
                printSuccessLine(isTTY, input, output, tgt, time.Since(compileStart), 0)
                return 0
        }

        binary, err := lnk.Link()
        if err != nil {
                fmt.Fprintf(os.Stderr, "error: linking failed: %s\n", err)
                return 1
        }

        if err := os.WriteFile(output, binary, 0755); err != nil {
                fmt.Fprintf(os.Stderr, "error: cannot write %s: %s\n", output, err)
                return 1
        }
        if verbose {
                phaseLine(isTTY, "Codegen", time.Since(phaseStart), fmt.Sprintf("%s + %s, %d functions compiled", tgt.Arch, tgt.Format, len(allCode)))
        }

        // Handle warnings
        if !warnNone && dh.WarningCount() > 0 {
                dh.RenderWarnings()
        }
        if werror && dh.WarningCount() > 0 {
                printAbortFooter(isTTY, dh)
                return 1
        }

        // Resolve absolute path for the output binary (needed for --run)
        absOutput, err := filepath.Abs(output)
        if err != nil {
                absOutput = output
        }

        // Get binary size for success message
        var binarySize int64
        if info, err := os.Stat(absOutput); err == nil {
                binarySize = info.Size()
        }

        if !quiet {
                printSuccessLine(isTTY, input, output, tgt, time.Since(compileStart), binarySize)
        }

        if runAfter {
                if !quiet {
                        base := filepath.Base(input)
                        if isTTY {
                                fmt.Fprintf(os.Stderr, "  %sRunning %s...%s\n", cliColorDim, base, cliColorReset)
                        } else {
                                fmt.Fprintf(os.Stderr, "  Running %s...\n", base)
                        }
                }
                return runBinary(absOutput, input)
        }
        return 0
}

// ========== AST Printer ==========

// printAST dumps the parsed AST to stderr in a readable, indented format.
func printAST(file *ast.File, dh *diag.DiagnosticHandler) {
        fmt.Fprintf(os.Stderr, "=== File: %s ===\n", file.Path)
        for _, d := range file.Decls {
                printDecl(d, 0)
        }
}

func printDecl(d ast.Decl, depth int) {
        indent := strings.Repeat("  ", depth)
        switch decl := d.(type) {
        case *ast.FnDecl:
                vis := "pub " + decl.Name
                if !decl.Public {
                        vis = decl.Name
                }
                fmt.Fprintf(os.Stderr, "%sfn %s(", indent, vis)
                for i, p := range decl.Params {
                        if i > 0 {
                                fmt.Fprint(os.Stderr, ", ")
                        }
                        fmt.Fprintf(os.Stderr, "%s %s", p.Type.Kind.String(), p.Name)
                        if p.Mutable {
                                fmt.Fprint(os.Stderr, " mut")
                        }
                }
                if decl.ReturnType != nil {
                        fmt.Fprintf(os.Stderr, ") -> %s", decl.ReturnType.Kind.String())
                } else {
                        fmt.Fprint(os.Stderr, ")")
                }
                if decl.Extern {
                        fmt.Fprintln(os.Stderr, " extern")
                } else {
                        fmt.Fprintln(os.Stderr, ":")
                }
                for _, s := range decl.Body {
                        printStmt(s, depth+1)
                }
        case *ast.ConstDecl:
                fmt.Fprintf(os.Stderr, "%sconst %s = ...\n", indent, decl.Name)
        case *ast.UseDecl:
                alias := decl.Module
                if decl.Alias != "" {
                        alias = decl.Alias
                }
                if len(decl.Symbols) > 0 {
                        syms := make([]string, len(decl.Symbols))
                        for i, s := range decl.Symbols {
                                syms[i] = s.Name
                                if s.Alias != "" {
                                        syms[i] += " as " + s.Alias
                                }
                        }
                        fmt.Fprintf(os.Stderr, "%sfrom %s use %s\n", indent, alias, strings.Join(syms, ", "))
                } else {
                        fmt.Fprintf(os.Stderr, "%suse %s\n", indent, alias)
                }
        case *ast.FFIUseDecl:
                fmt.Fprintf(os.Stderr, "%suse ffi:%s\n", indent, decl.Module)
        }
}

func printStmt(s ast.Stmt, depth int) {
        indent := strings.Repeat("  ", depth)
        switch stmt := s.(type) {
        case *ast.LetStmt:
                mut := ""
                if stmt.Mutable {
                        mut = "mut "
                }
                fmt.Fprintf(os.Stderr, "%s%s%s = ...\n", indent, mut, stmt.Name)
        case *ast.ExprStmt:
                fmt.Fprintf(os.Stderr, "%s...\n", indent)
        case *ast.ReturnStmt:
                if stmt.Value != nil {
                        fmt.Fprintf(os.Stderr, "%sreturn ...\n", indent)
                } else {
                        fmt.Fprintf(os.Stderr, "%sreturn\n", indent)
                }
        case *ast.IfStmt:
                fmt.Fprintf(os.Stderr, "%sif ...:\n", indent)
                for _, br := range stmt.Branches {
                        for _, st := range br.Body {
                                printStmt(st, depth+1)
                        }
                }
                if len(stmt.Else) > 0 {
                        fmt.Fprintf(os.Stderr, "%selse:\n", indent)
                        for _, st := range stmt.Else {
                                printStmt(st, depth+1)
                        }
                }
        case *ast.WhileStmt:
                fmt.Fprintf(os.Stderr, "%swhile ...:\n", indent)
                for _, st := range stmt.Body {
                        printStmt(st, depth+1)
                }
        case *ast.ForStmt:
                fmt.Fprintf(os.Stderr, "%sfor ...\n", indent)
                for _, st := range stmt.Body {
                        printStmt(st, depth+1)
                }
        case *ast.MatchStmt:
                fmt.Fprintf(os.Stderr, "%smatch ...:\n", indent)
                for _, cs := range stmt.Cases {
                        for _, st := range cs.Body {
                                printStmt(st, depth+1)
                        }
                }
        case *ast.AssertStmt:
                fmt.Fprintf(os.Stderr, "%sassert ...\n", indent)
        case *ast.BreakStmt:
                fmt.Fprintf(os.Stderr, "%sbreak\n", indent)
        case *ast.ContinueStmt:
                fmt.Fprintf(os.Stderr, "%scontinue\n", indent)
        }
}

// ========== IR Lowering (One-Pass) ==========

// lowerToIR converts the checked program into IR. This is the core one-pass
// compilation step: AST is walked once, IR is emitted directly.
func lowerToIR(cp *check.CheckedProgram) *ir.Module {
        module := ir.NewModule()

        // Build set of user-defined function names for the lowerer.
        userFuncs := make(map[string]bool, len(cp.Functions))
        for name := range cp.Functions {
                userFuncs[name] = true
        }

        // Build set of variadic user functions: fn name → number of non-variadic params.
        variadicFuncs := make(map[string]int) // func name → fixed param count (excl variadic)
        for name, fn := range cp.Functions {
                if len(fn.Params) > 0 && fn.Params[len(fn.Params)-1].Variadic {
                        variadicFuncs[name] = len(fn.Params) - 1
                }
        }

        // Build map of default parameter expressions: func name → defaults per param.
        defaultParams := make(map[string][]ast.Expr)
        for name, fn := range cp.Functions {
                if decl, ok := fn.Decl.(*ast.FnDecl); ok {
                        defaults := make([]ast.Expr, len(decl.Params))
                        for i, p := range decl.Params {
                                defaults[i] = p.Default
                        }
                        defaultParams[name] = defaults
                }
        }

        // Build import alias map: alias → original module name.
        importAliases := make(map[string]string, len(cp.Imports))
        for _, imp := range cp.Imports {
                if imp.Alias != "" {
                        importAliases[imp.Alias] = imp.ModuleName
                }
        }

        // Lower functions
        for name, fn := range cp.Functions {
                if fn.Extern {
                        f := module.AddFunc(name, ir.VRaw, fn.Public, true)
                        for _, p := range fn.Params {
                                f.NewParam(module.AllocID(), ir.VRaw, p.Name)
                        }
                        continue
                }

                retType := ir.VVoid
                if fn.RetType.Kind != check.TVoid {
                        retType = ir.VRaw
                }

                f := module.AddFunc(name, retType, fn.Public, false)
                for _, p := range fn.Params {
                        f.NewParam(module.AllocID(), ir.VRaw, p.Name)
                }

                b := ir.NewBuilder(module, f)
                lower := &lowerer{
                        builder:       b,
                        fn:            f,
                        locals:        make(map[string]*ir.Value),
                        exprTypes:     cp.ExprTypes,
                        enumInfos:     cp.EnumInfos,
                        module:        module,
                        anonNames:     make(map[string]string),
                        anonCaptures:  make(map[string]*closureInfo),
                        curFnName:     name,
                        userFuncs:     userFuncs,
                        variadicFuncs: variadicFuncs,
                        defaultParams: defaultParams,
                        consts:        make(map[string]ast.Expr, len(cp.Consts)),
                        mutableGlobals: make(map[string]*ir.Value),
                        importAliases: importAliases,
                }

                // Seed const table from checked program.
                for cn, cc := range cp.Consts {
                        if cc.Mutable {
                                // Mutable top-level variable: create a table to hold the value.
                                // The table is created once and its entries are mutated.
                                // On read: table_get(globalTable, key)
                                // On write: table_set(globalTable, key, val)
                                initVal := lower.expr(cc.Value)
                                tbl := b.TableNew(1, "mut_global_"+cn)
                                keyStr := b.ConstStr(cn, "mut_global_key")
                                keyLen := b.ConstRawInt(int64(len(cn)), "mut_global_key.len")
                                keyStrVal := b.Call("y_str_new", []*ir.Value{keyStr, keyLen}, ir.VRaw, "mut_global_key.str")
                                b.TableSet(tbl, keyStrVal, initVal)
                                lower.mutableGlobals[cn] = tbl
                        } else {
                                lower.consts[cn] = cc.Value
                        }
                }

                // Bind parameters as locals
                for i, p := range fn.Params {
                        lower.locals[p.Name] = f.Params[i]
                }

                // Lower body
                for _, s := range fn.Body {
                        lower.stmt(s)
                }

                // Ensure terminator
                if b.CurBlock() != nil && !b.CurBlock().IsTerminated() {
                        b.Return(nil)
                }
        }

        return module
}

// lowerer holds state for one-pass IR lowering.
type lowerer struct {
        builder       *ir.Builder
        fn            *ir.Func
        locals        map[string]*ir.Value
        mutables      map[string]*ir.Value // mutable var name → original ir.Value (fixed slot)
        loopEnd       *ir.Block
        loopCond      *ir.Block
        blockID       int
        exprTypes     map[ast.Expr]check.TypeDesc // type of every checked expression
        enumInfos     map[string]*check.EnumInfo  // enum name → variant info
        module        *ir.Module                   // for creating anonymous functions
        anonCount     int                         // counter for generating unique anon func names
        anonNames     map[string]string           // local var name → generated IR function name
        anonCaptures  map[string]*closureInfo // local var name → closure metadata (captures, fn name, etc.)
        curFnName     string                      // name of the enclosing function (for generated names)
        userFuncs     map[string]bool             // set of user-defined function names
        variadicFuncs map[string]int              // variadic func name → fixed param count
        defaultParams map[string][]ast.Expr        // func name → default expr per param (nil = no default)
        consts        map[string]ast.Expr          // module-level const name → value expression
        mutableGlobals map[string]*ir.Value        // mutable global name → table Value (for mutable top-level vars)
        importAliases map[string]string           // alias → original module name (for 'use sys as s')
}

// closureInfo records metadata about a closure created from an anonymous function.
// It tracks which outer-scope variables are captured so that call sites can
// load the captured values from the closure struct and pass them as extra args.
type closureInfo struct {
        irFuncName string   // the generated IR function name (e.g. "main.__anon_0")
        captures   []string // ordered list of captured variable names
        captureVals []*ir.Value // the IR values of the captured variables at closure creation time
        nCaptures int      // len(captures)
}

// isFloat returns true if the AST expression has float type.
func (l *lowerer) isFloat(e ast.Expr) bool {
        if l.exprTypes == nil {
                return false
        }
        td, ok := l.exprTypes[e]
        return ok && td.Kind == check.TFp
}

// tableStrKey creates a tagged string key for table operations, converting
// the given string to a y_str_new call result. This is used by struct and
// enum lowering to set/get fields by name.
func (l *lowerer) tableStrKey(b *ir.Builder, key string) *ir.Value {
        k := b.ConstStr(key, "key."+key)
        kLen := b.ConstRawInt(int64(len(key)), "key."+key+".len")
        return b.Call("y_str_new", []*ir.Value{k, kLen}, ir.VRaw, "key."+key+".str")
}

// isStr returns true if the AST expression has string type.
func (l *lowerer) isStr(e ast.Expr) bool {
        if l.exprTypes == nil {
                return false
        }
        td, ok := l.exprTypes[e]
        return ok && td.Kind == check.TStr
}

// isStrExpr reports whether the given expression has type str.
// Used to decide whether to use content-based equality (pure_values_equal)
// for == and != operators, since bitwise comparison of two distinct string
// allocations always returns false even when the content is identical.
func (l *lowerer) isStrExpr(e ast.Expr) bool {
        return l.isStr(e)
}

// isBoolExpr reports whether the given expression has type bool.
// Used to decide whether `not` should use XOR-with-1 (for bools, to
// preserve the tag bits) or bitwise NOT (for integers).
func (l *lowerer) isBoolExpr(e ast.Expr) bool {
        if l.exprTypes == nil {
                return false
        }
        td, ok := l.exprTypes[e]
        return ok && td.Kind == check.TBool
}

func (l *lowerer) stmt(s ast.Stmt) {
        switch s := s.(type) {
        case *ast.ConstStmt:
                // Local const: evaluate the constant expression and bind it
                val := l.expr(s.Value)
                l.locals[s.Name] = val
        case *ast.AssertStmt:
                l.lowerAssert(s)
        case *ast.LetStmt:
                // Check if the value is an anonymous function expression.
                // If so, create a separate IR function for it and record the
                // mapping so that calls through this variable use a direct call.
                if fnExpr, ok := s.Value.(*ast.FnExpr); ok {
                        anonName := fmt.Sprintf("%s.__anon_%d", l.curFnName, l.anonCount)
                        l.anonCount++
                        captures := l.lowerAnonFn(fnExpr, anonName, s.Name)
                        // Sync the main builder's register counter with the module's.
                        // lowerAnonFn creates an anon builder that advances module.nextRegID;
                        // without this sync, the main builder would reuse stale IDs.
                        l.builder.SyncFromModule()
                        l.anonNames[s.Name] = anonName

                        // Always allocate a closure struct so the tagged value
                                // carries a valid fn_ptr. This is required when the
                                // closure is returned from a function and called
                                // from a different scope (the caller won't have
                                // anonNames, so it goes through the trampoline).
                                // Layout: [fn_ptr, n_captures, capture_0, capture_1, ...]
                                // Index 0 = fn_ptr (resolved by linker via PLT32 relocation)
                                // Index 1 = n_captures
                                // Index 2+i = capture[i]
                                nCap := len(captures)
                                nCapVal := l.builder.ConstRawInt(int64(nCap), "n_cap")
                                closurePtr := l.builder.Call("y_closure_new", []*ir.Value{nCapVal}, ir.VRaw, "closure_ptr")

                                // Store fn_ptr at index 0 via linker relocation.
                                l.builder.Call("y_closure_set_fnptr_"+anonName, []*ir.Value{closurePtr}, ir.VVoid, "set_fnptr")

                                // Store n_captures at index 1
                                nCapIdx := l.builder.ConstRawInt(1, "idx_1")
                                l.builder.Call("y_closure_set", []*ir.Value{closurePtr, nCapIdx, nCapVal}, ir.VVoid, "set_ncap")

                                // Store each captured value at index 2+i
                                for i, capName := range captures {
                                        if capVal, ok := l.locals[capName]; ok {
                                                idxVal := l.builder.ConstRawInt(int64(2+i), fmt.Sprintf("idx_%d", 2+i))
                                                l.builder.Call("y_closure_set", []*ir.Value{closurePtr, idxVal, capVal}, ir.VVoid, fmt.Sprintf("set_cap_%s", capName))
                                        }
                                }

                                // Tag closure pointer as TAG_FUNC
                                closureTagged := l.builder.Tag(closurePtr, ir.TagFunc, "closure_tagged")

                                l.locals[s.Name] = closureTagged
                                if len(captures) > 0 {
                                        l.anonCaptures[s.Name] = &closureInfo{
                                                irFuncName: anonName,
                                                captures:   captures,
                                                nCaptures:  nCap,
                                        }
                                }
                        return
                }
                val := l.expr(s.Value)
                if s.Mutable {
                        // Mutable variables need a non-constant SSA value with
                        // its own slot that persists across loop iterations.
                        // Wrap the initial value in Or(val, 0) to force a slot.
                        zero := l.builder.ConstInt(0, "")
                        wrapped := l.builder.Or(val, zero, "mut."+s.Name)
                        l.builder.CurBlock().MarkNoFold()
                        l.locals[s.Name] = wrapped
                        if l.mutables == nil {
                                l.mutables = make(map[string]*ir.Value)
                        }
                        l.mutables[s.Name] = wrapped
                } else {
                        l.locals[s.Name] = val
                }

        case *ast.TupleDestructStmt:
                // let (a, b, c) = expr — destructure a tuple (backed by table with int keys)
                b := l.builder
                tupleVal := l.expr(s.Value)
                for i, name := range s.Names {
                        key := b.ConstInt(int64(i), "destructure.idx")
                        elem := b.TableGet(tupleVal, key, "destructure."+name)
                        if s.Mutable {
                                wrapped := b.Or(elem, b.ConstInt(0, ""), "mut."+name)
                                b.CurBlock().MarkNoFold()
                                if l.mutables == nil {
                                        l.mutables = make(map[string]*ir.Value)
                                }
                                l.mutables[name] = wrapped
                                l.locals[name] = wrapped
                        } else {
                                l.locals[name] = elem
                        }
                }

        case *ast.ExprStmt:
                l.expr(s.Expr)

        case *ast.ReturnStmt:
                if s.Value != nil {
                        b := l.builder
                        val := l.expr(s.Value)
                        b.Return(val)
                } else {
                        l.builder.Return(nil)
                }

        case *ast.IfStmt:
                l.ifStmt(s)

        case *ast.WhileStmt:
                l.whileStmt(s)

        case *ast.ForStmt:
                l.forStmt(s)

        case *ast.MatchStmt:
                l.matchStmt(s)

        case *ast.BreakStmt:
                if l.loopEnd != nil {
                        l.builder.Jump(l.loopEnd, nil)
                }

        case *ast.ContinueStmt:
                if l.loopCond != nil {
                        l.builder.Jump(l.loopCond, nil)
                }
        }
}

func (l *lowerer) ifStmt(s *ast.IfStmt) {
        b := l.builder
        l.blockID++
        baseID := l.blockID
        mergeBlock := l.fn.AddBlock(fmt.Sprintf("if.merge%d", baseID))

        for i, branch := range s.Branches {
                thenBlock := l.fn.AddBlock(fmt.Sprintf("if.then%d.%d", baseID, i))
                elseBlock := l.fn.AddBlock(fmt.Sprintf("if.else%d.%d", baseID, i))

                cond := l.expr(branch.Cond)
                b.Branch(cond, thenBlock, elseBlock, nil, nil)

                b.SetBlock(thenBlock)
                for _, st := range branch.Body {
                        l.stmt(st)
                }
                if !b.CurBlock().IsTerminated() {
                        b.Jump(mergeBlock, nil)
                }

                b.SetBlock(elseBlock)
        }

        if len(s.Else) > 0 {
                for _, st := range s.Else {
                        l.stmt(st)
                }
        }

        if !b.CurBlock().IsTerminated() {
                b.Jump(mergeBlock, nil)
        }
        b.SetBlock(mergeBlock)
}

func (l *lowerer) whileStmt(s *ast.WhileStmt) {
        b := l.builder
        l.blockID++
        baseID := l.blockID
        condBlock := l.fn.AddBlock(fmt.Sprintf("while.cond%d", baseID))
        bodyBlock := l.fn.AddBlock(fmt.Sprintf("while.body%d", baseID))
        endBlock := l.fn.AddBlock(fmt.Sprintf("while.end%d", baseID))

        b.Jump(condBlock, nil)

        b.SetBlock(condBlock)
        cond := l.expr(s.Cond)
        b.Branch(cond, bodyBlock, endBlock, nil, nil)

        b.SetBlock(bodyBlock)
        oldEnd, oldCond := l.loopEnd, l.loopCond
        l.loopEnd = endBlock
        l.loopCond = condBlock
        for _, st := range s.Body {
                l.stmt(st)
        }
        l.loopEnd, l.loopCond = oldEnd, oldCond
        b.Jump(condBlock, nil)

        b.SetBlock(endBlock)
}

// forRangeStmt lowers a range for-loop (for i in low..high) by emitting:
//
//   __rN = low              (Or + NoFold → stack slot)
//   while __rN < high      (Lt reads from slot)
//       i = __rN           (initVal = slot read)
//       body
//       __rN = __rN + 1    (Add from slot, Copy back to slot)
//       goto while
//
// The Or(val, 0, name) pattern forces the x86_64 codegen to allocate a
// named stack slot. All reads of that IR Value load from the slot.
// The Copy instruction writes a new value to the slot.
func (l *lowerer) forRangeStmt(s *ast.ForStmt, rng *ast.RangeExpr) {
        b := l.builder

        // Evaluate bounds once.
        lowVal := l.expr(rng.Low)
        highVal := l.expr(rng.High)

        // Create a mutable counter variable via Or + NoFold (→ stack slot).
        counterName := "__range_i_" + strconv.Itoa(l.blockID+1)
        initVal := b.Or(lowVal, b.ConstInt(0, ""), counterName)
        b.CurBlock().MarkNoFold()

        l.blockID++
        baseID := l.blockID
        condBlock := l.fn.AddBlock(fmt.Sprintf("range.cond%d", baseID))
        bodyBlock := l.fn.AddBlock(fmt.Sprintf("range.body%d", baseID))
        incBlock := l.fn.AddBlock(fmt.Sprintf("range.inc%d", baseID))
        endBlock := l.fn.AddBlock(fmt.Sprintf("range.end%d", baseID))

        b.Jump(condBlock, nil)

        // Condition: counter < high (reads initVal from stack slot)
        b.SetBlock(condBlock)
        lt := b.Lt(initVal, highVal, "range.lt")
        b.Branch(lt, bodyBlock, endBlock, nil, nil)

        // Body: bind user variable to counter (reads from slot)
        b.SetBlock(bodyBlock)
        if s.Key != "" {
                l.locals[s.Key] = initVal
        }

        oldEnd, oldCond := l.loopEnd, l.loopCond
        l.loopEnd = endBlock
        l.loopCond = incBlock // continue → increment (not condition) for range-for
        for _, st := range s.Body {
                l.stmt(st)
        }
        l.loopEnd, l.loopCond = oldEnd, oldCond

        // Increment: counter = counter + 1
        b.Jump(incBlock, nil)
        b.SetBlock(incBlock)
        one := b.ConstInt(1, "one")
        newVal := b.Add(initVal, one, "range.inc")
        b.Copy(newVal, initVal) // write back to slot

        b.Jump(condBlock, nil)
        b.SetBlock(endBlock)
}

// forStrStmt lowers a string for-loop (for i in str_expr) by desugaring
// to a range-for over the string's byte length.
//
//   for i in s
//     body
//
// becomes:
//
//   __str = s              (evaluate string once)
//   __len = y_str_len(__str)
//   __rN = 0               (Or + NoFold → stack slot)
//   while __rN < __len
//     i = __rN              (bind loop variable)
//     body
//     __rN = __rN + 1
//     goto while
func (l *lowerer) forStrStmt(s *ast.ForStmt) {
        b := l.builder

        // Evaluate the string expression once.
        strVal := l.expr(s.Over)

        // Get the string length.
        lenVal := b.Call("y_str_len", []*ir.Value{strVal}, ir.VTagged, "str.len")

        // Create a mutable counter variable via Or + NoFold (→ stack slot).
        counterName := "__str_i_" + strconv.Itoa(l.blockID+1)
        initVal := b.Or(b.ConstInt(0, ""), b.ConstInt(0, ""), counterName)
        b.CurBlock().MarkNoFold()

        l.blockID++
        baseID := l.blockID
        condBlock := l.fn.AddBlock(fmt.Sprintf("str.cond%d", baseID))
        bodyBlock := l.fn.AddBlock(fmt.Sprintf("str.body%d", baseID))
        incBlock := l.fn.AddBlock(fmt.Sprintf("str.inc%d", baseID))
        endBlock := l.fn.AddBlock(fmt.Sprintf("str.end%d", baseID))

        b.Jump(condBlock, nil)

        // Condition: counter < string length
        b.SetBlock(condBlock)
        lt := b.Lt(initVal, lenVal, "str.lt")
        b.Branch(lt, bodyBlock, endBlock, nil, nil)

        // Body: bind user variable to counter
        b.SetBlock(bodyBlock)
        if s.Key != "" {
                l.locals[s.Key] = initVal
        }

        oldEnd, oldCond := l.loopEnd, l.loopCond
        l.loopEnd = endBlock
        l.loopCond = incBlock
        for _, st := range s.Body {
                l.stmt(st)
        }
        l.loopEnd, l.loopCond = oldEnd, oldCond

        // Increment: counter = counter + 1
        b.Jump(incBlock, nil)
        b.SetBlock(incBlock)
        one := b.ConstInt(1, "one")
        newVal := b.Add(initVal, one, "str.inc")
        b.Copy(newVal, initVal)

        b.Jump(condBlock, nil)
        b.SetBlock(endBlock)
}

func (l *lowerer) forStmt(s *ast.ForStmt) {
        // Check if this is a range for-loop: for i in low..high
        if rng, ok := s.Over.(*ast.RangeExpr); ok {
                l.forRangeStmt(s, rng)
                return
        }

        // Check if this is a string for-loop: for ch in "string"
        if overType, ok := l.exprTypes[s.Over]; ok && overType.Kind == check.TStr {
                l.forStrStmt(s)
                return
        }

        b := l.builder
        tab := l.expr(s.Over)

        // Use a mutable local variable for the iterator index.
        // We cannot use StackAlloc + Store because Store dereferences through
        // the pointer and clobbers it after the first write. Instead, we
        // keep the index in a regular mutable slot (Or(id, zero) pattern).
        idxVar := "__for_idx_" + strconv.Itoa(l.blockID+1)
        zeroVal := b.ConstInt(0, "zero")
        wrappedZero := b.Or(zeroVal, b.ConstInt(0, ""), idxVar)
        b.CurBlock().MarkNoFold()
        if l.mutables == nil {
                l.mutables = make(map[string]*ir.Value)
        }
        l.mutables[idxVar] = wrappedZero

        l.blockID++
        baseID := l.blockID
        condBlock := l.fn.AddBlock(fmt.Sprintf("for.cond%d", baseID))
        bodyBlock := l.fn.AddBlock(fmt.Sprintf("for.body%d", baseID))
        endBlock := l.fn.AddBlock(fmt.Sprintf("for.end%d", baseID))

        b.Jump(condBlock, nil)

        b.SetBlock(condBlock)
        // Advance to the next occupied entry, then check validity.
        next := b.Call("y_tab_iter_next", []*ir.Value{tab, wrappedZero}, ir.VRaw, "iter.next")
        // Update the mutable slot: idxVar = Or(next, 0) to force slot persistence
        wrappedNext := b.Or(next, b.ConstInt(0, ""), idxVar)
        b.CurBlock().MarkNoFold()
        l.mutables[idxVar] = wrappedNext

        valid := b.Call("y_tab_iter_valid", []*ir.Value{tab, wrappedNext}, ir.VRaw, "iter.valid")
        b.Branch(valid, bodyBlock, endBlock, nil, nil)

        b.SetBlock(bodyBlock)
        if s.Key != "" {
                key := b.Call("y_tab_iter_key", []*ir.Value{tab, wrappedNext}, ir.VRaw, "iter.key")
                l.locals[s.Key] = key
        }
        if s.Value != "" {
                val := b.Call("y_tab_iter_val", []*ir.Value{tab, wrappedNext}, ir.VRaw, "iter.val")
                l.locals[s.Value] = val
        }

        oldEnd, oldCond := l.loopEnd, l.loopCond
        l.loopEnd = endBlock
        l.loopCond = condBlock
        for _, st := range s.Body {
                l.stmt(st)
        }
        l.loopEnd, l.loopCond = oldEnd, oldCond

        b.Jump(condBlock, nil)

        b.SetBlock(endBlock)
}

// lowerAssert lowers an assert statement to:
//   if !cond { y_error("assertion failed: msg"); y_sys_exit(1) }
func (l *lowerer) lowerAssert(s *ast.AssertStmt) {
        b := l.builder
        l.blockID++
        baseID := l.blockID

        failBlock := l.fn.AddBlock(fmt.Sprintf("assert.fail%d", baseID))
        mergeBlock := l.fn.AddBlock(fmt.Sprintf("assert.ok%d", baseID))

        cond := l.expr(s.Cond)
        b.Branch(cond, mergeBlock, failBlock, nil, nil)

        b.SetBlock(failBlock)

        // Build error message
        msg := "assertion failed"
        if s.Message != nil {
                // If message is a string literal, include it
                if sl, ok := s.Message.(*ast.StringLit); ok {
                        msg = "assertion failed: " + sl.Value
                } else {
                        // For non-literal messages, use a runtime call
                        msgVal := l.expr(s.Message)
                        prefixVal := b.ConstStr("assertion failed: ", "assert.prefix")
                        prefixLen := b.ConstRawInt(int64(len("assertion failed: ")), "assert.prefix.len")
                        prefix := b.Call("y_str_new", []*ir.Value{prefixVal, prefixLen}, ir.VRaw, "prefix")
                        msgVal = b.Call("y_str_concat", []*ir.Value{prefix, msgVal}, ir.VRaw, "assert.msg")
                        b.Call("y_error", []*ir.Value{msgVal}, ir.VRaw, "")
                        b.Call("y_sys_exit", []*ir.Value{b.ConstInt(1, "")}, ir.VRaw, "")
                        b.SetBlock(mergeBlock)
                        return
                }
        }

        msgVal := b.ConstStr(msg, "assert.msg")
        msgLen := b.ConstRawInt(int64(len(msg)), "assert.msg.len")
        strVal := b.Call("y_str_new", []*ir.Value{msgVal, msgLen}, ir.VRaw, "assert.str")
        b.Call("y_error", []*ir.Value{strVal}, ir.VRaw, "")
        b.Call("y_sys_exit", []*ir.Value{b.ConstInt(1, "")}, ir.VRaw, "")

        b.SetBlock(mergeBlock)
}

// isEnumBinOp reports whether a binary operation is comparing two enum-typed
// values.  Used to select structural equality over pointer identity.
func (l *lowerer) isEnumBinOp(e *ast.BinOp) bool {
        if l.exprTypes == nil {
                return false
        }
        lt, ok := l.exprTypes[e.Left]
        if !ok || lt.Kind != check.TEnum {
                return false
        }
        rt, ok := l.exprTypes[e.Right]
        return ok && rt.Kind == check.TEnum
}

// lowerEnumEq emits IR for enum equality/inequality.
// Delegates to y_enum_eq runtime function which compares _v and _p fields.
func (l *lowerer) lowerEnumEq(e *ast.BinOp, left, right *ir.Value) *ir.Value {
        b := l.builder
        result := b.Call("y_enum_eq", []*ir.Value{left, right}, ir.VRaw, "enum_eq")
        if e.Op == ast.TNeq {
                result = b.Not(result, "enum_neq")
        }
        return result
}

func (l *lowerer) matchStmt(s *ast.MatchStmt) {
        b := l.builder
        l.blockID++
        baseID := l.blockID
        mergeBlock := l.fn.AddBlock(fmt.Sprintf("match.merge%d", baseID))
        subject := l.expr(s.Subject)

        // Optimization for enum match: instead of comparing full tables with
        // Eq (structural equality), extract the "_v" field and compare the
        // integer variant index. This is O(1) per case instead of O(n).
        subjectIsEnum := false
        _ = ""
        if td, ok := l.exprTypes[s.Subject]; ok && td.Kind == check.TEnum {
                subjectIsEnum = true
                _ = td.Name
        }

        var subjectVariant *ir.Value
        if subjectIsEnum {
                vKey := l.tableStrKey(b, "_v")
                subjectVariant = b.TableGet(subject, vKey, "match.enum._v")
        }

        for i, mc := range s.Cases {
                caseBlock := l.fn.AddBlock(fmt.Sprintf("match.case%d.%d", baseID, i))
                nextBlock := l.fn.AddBlock(fmt.Sprintf("match.next%d.%d", baseID, i))

                var eq *ir.Value
                var bindVar string
                if subjectIsEnum {
                        // Enum-optimized path: compare variant indices.
                        // Handle both EnumLit and EnumMatchPattern.
                        var matchEnumName, matchVariantName string
                        switch v := mc.Value.(type) {
                        case *ast.EnumLit:
                                matchEnumName = v.EnumName
                                matchVariantName = v.VariantName
                        case *ast.EnumMatchPattern:
                                matchEnumName = v.EnumName
                                matchVariantName = v.VariantName
                                bindVar = v.BindVar
                        }
                        if matchEnumName != "" && l.enumInfos != nil {
                                if ei, ok := l.enumInfos[matchEnumName]; ok {
                                        if idx, ok := ei.VariantIndex[matchVariantName]; ok {
                                                caseIdx := b.ConstInt(int64(idx), "match.enum.idx")
                                                eq = b.Eq(subjectVariant, caseIdx, "match.eq.enum")
                                        }
                                }
                        }
                }
                if eq == nil {
                        // Fallback: generic equality comparison.
                        caseVal := l.expr(mc.Value)
                        eq = b.Eq(subject, caseVal, "match.eq")
                }
                b.Branch(eq, caseBlock, nextBlock, nil, nil)

                b.SetBlock(caseBlock)
                // Extract payload binding if this is a destructuring pattern.
                if bindVar != "" && subjectIsEnum && l.enumInfos != nil {
                        pKey := l.tableStrKey(b, "_p")
                        payload := b.TableGet(subject, pKey, "match.enum._p")
                        l.locals[bindVar] = payload
                }
                for _, st := range mc.Body {
                        l.stmt(st)
                }
                if !b.CurBlock().IsTerminated() {
                        b.Jump(mergeBlock, nil)
                }

                b.SetBlock(nextBlock)
        }

        if len(s.Default) > 0 {
                for _, st := range s.Default {
                        l.stmt(st)
                }
        }
        if !b.CurBlock().IsTerminated() {
                b.Jump(mergeBlock, nil)
        }
        b.SetBlock(mergeBlock)
}

func (l *lowerer) expr(e ast.Expr) *ir.Value {
        b := l.builder

        switch e := e.(type) {
        case *ast.IntLit:
                return b.ConstInt(e.Value, "")

        case *ast.FloatLit:
                return b.Call("y_fp_new", []*ir.Value{b.ConstFp(e.Value, "")}, ir.VRaw, "fp")

        case *ast.StringLit:
                // String literals: call y_str_new(data_ptr, len) to allocate a
                // tagged string.  The ConstStr IR value holds the string; we pass it
                // to y_str_new as two arguments (pointer to data + length) and
                // get back a tagged string value (tag=4, payload=pointer).
                //
                // NOTE: y_str_new expects the actual string data bytes in the
                // binary's data section.  For now, we emit the string length as
                // the first arg and rely on the y_str_new implementation to
                // handle inline string data via the ConstStr mechanism.
                //
                // The result of y_str_new is already a tagged string value, so
                // the caller (e.g. print) should NOT re-tag it.
                strVal := b.ConstStr(e.Value, "str.data")
                lenVal := b.ConstRawInt(int64(len(e.Value)), "str.len")
                return b.Call("y_str_new", []*ir.Value{strVal, lenVal}, ir.VRaw, "str")

        case *ast.InterpStr:
                return l.lowerInterpStr(e, b)

        case *ast.BoolLit:
                return b.ConstBool(e.Value, "")

        case *ast.NilLit:
                return b.ConstNil("")

        case *ast.Ident:
                if v, ok := l.locals[e.Name]; ok {
                        return v
                }
                // Check mutable globals (table-backed)
                if gt, ok := l.mutableGlobals[e.Name]; ok {
                        keyStr := b.ConstStr(e.Name, "gkey."+e.Name)
                        keyLen := b.ConstRawInt(int64(len(e.Name)), "gkey."+e.Name+".len")
                        keyStrVal := b.Call("y_str_new", []*ir.Value{keyStr, keyLen}, ir.VRaw, "gkey."+e.Name+".str")
                        return b.TableGet(gt, keyStrVal, "gval."+e.Name)
                }
                // Check module-level consts
                if ce, ok := l.consts[e.Name]; ok {
                        return l.expr(ce)
                }
                return b.ConstInt(0, "unknown")

        case *ast.BinOp:
                left := l.expr(e.Left)
                switch e.Op {
                case ast.TAnd:
                        return l.shortCircuitAnd(left, e.Right)
                case ast.TOr:
                        return l.shortCircuitOr(left, e.Right)
                }
                right := l.expr(e.Right)
                // String concatenation: str + str → y_str_concat(a, b).
                // Also handles str + <non-str> via auto-coercion in the checker.
                if e.Op == ast.TPlus && l.isStr(e) {
                        return b.Call("y_str_concat", []*ir.Value{left, right}, ir.VRaw, "strcat")
                }
                // String equality: str == str and str != str must use
                // content-based comparison (pure_values_equal), not bitwise
                // pointer comparison.  Two separate allocations of "int" have
                // different StrHeader pointers, so bitwise == would always
                // return false for them.  y_val_eq is bitwise; we need the
                // content-aware pure_values_equal instead.
                //
                // Note: isStr(e) checks the type of e itself, which for ==
                // is bool (not str).  We need to check the OPERAND types.
                if e.Op == ast.TEq || e.Op == ast.TNeq {
                        if l.isStrExpr(e.Left) || l.isStrExpr(e.Right) {
                                eqResult := b.Call("pure_values_equal", []*ir.Value{left, right}, ir.VRaw, "streq")
                                if e.Op == ast.TEq {
                                        return eqResult
                                }
                                // For !=, invert the result: 1 -> 0, 0 -> 1.
                                return b.Xor(eqResult, b.ConstInt(1, "one"), "strneq")
                        }
                }
                // For float-typed expressions, delegate to runtime float functions.
                if l.isFloat(e) {
                        switch e.Op {
                        case ast.TPlus:
                                return b.Call("y_fp_add", []*ir.Value{left, right}, ir.VRaw, "fadd")
                        case ast.TMinus:
                                return b.Call("y_fp_sub", []*ir.Value{left, right}, ir.VRaw, "fsub")
                        case ast.TStar:
                                return b.Call("y_fp_mul", []*ir.Value{left, right}, ir.VRaw, "fmul")
                        case ast.TSlash:
                                return b.Call("y_fp_div", []*ir.Value{left, right}, ir.VRaw, "fdiv")
                        }
                }
                // Enum equality: compare _v and _p fields structurally
                // instead of using pointer-identity y_val_eq.
                if (e.Op == ast.TEq || e.Op == ast.TNeq) && l.isEnumBinOp(e) {
                        return l.lowerEnumEq(e, left, right)
                }
                switch e.Op {
                case ast.TPlus:
                        return b.Add(left, right, "add")
                case ast.TMinus:
                        return b.Sub(left, right, "sub")
                case ast.TStar:
                        return b.Mul(left, right, "mul")
                case ast.TSlash:
                        return b.Div(left, right, "div")
                case ast.TPercent:
                        return b.Mod(left, right, "mod")
                case ast.TAmp:
                        return b.And(left, right, "and")
                case ast.TPipe:
                        return b.Or(left, right, "or")
                case ast.TCaret:
                        return b.Xor(left, right, "xor")
                case ast.TLShift:
                        return b.Shl(left, right, "shl")
                case ast.TRShift:
                        return b.Shr(left, right, "shr")
                case ast.TEq:
                        return b.Eq(left, right, "eq")
                case ast.TNeq:
                        return b.Neq(left, right, "neq")
                case ast.TLt:
                        return b.Lt(left, right, "lt")
                case ast.TLe:
                        return b.Le(left, right, "le")
                case ast.TGt:
                        return b.Gt(left, right, "gt")
                case ast.TGe:
                        return b.Ge(left, right, "ge")
                }
                return left

        case *ast.UnaryOp:
                operand := l.expr(e.Operand)
                // For float negation, delegate to runtime.
                if l.isFloat(e) && e.Op == ast.TMinus {
                        return b.Call("y_fp_neg", []*ir.Value{operand}, ir.VRaw, "fneg")
                }
                switch e.Op {
                case ast.TMinus:
                        return b.Neg(operand, "neg")
                case ast.TNot:
                        // Bool NOT: must XOR the payload bit (1), NOT bitwise-NOT
                        // the whole tagged value.  Yilt bools are tagged:
                        //   true  = (TAG_BOOL << 56) | 1
                        //   false = (TAG_BOOL << 56) | 0
                        // Bitwise NOT would corrupt the tag bits and produce
                        // garbage.  XOR-with-1 flips just the payload bit,
                        // turning true into false and vice versa.
                        if l.isBoolExpr(e.Operand) {
                                return b.Xor(operand, b.ConstInt(1, "one"), "boolnot")
                        }
                        // For non-bool (e.g. int), fall back to bitwise NOT.
                        return b.Not(operand, "not")
                case ast.TTilde:
                        return b.BitNot(operand, "bitnot")
                }
                return operand

        case *ast.CallExpr:
                var fnName string
                var methodReceiver *ir.Value // non-nil for value method calls (s.len(), t.get())
                var indirectCallee *ir.Value // non-nil for indirect calls through local function variables
                switch fn := e.Func.(type) {
                case *ast.Ident:
                        // Check if this local variable was bound to an anonymous function.
                        // If so, use a direct call to the generated function name.
                        if anonName, ok := l.anonNames[fn.Name]; ok {
                                fnName = anonName
                        } else {
                                fnName = fn.Name
                                // If the callee name refers to a local variable (including
                                // function parameters) that holds a function value, use an
                                // indirect call.  Named functions are NOT in locals.
                                if _, isLocal := l.locals[fn.Name]; isLocal {
                                        indirectCallee = l.expr(e.Func)
                                }
                        }
                case *ast.MemberExpr:
                        // Try to resolve as a value method call on a typed receiver
                        // (e.g. s.len() → y_len(s), t.get(key) → y_tab_get(t, key)).
                        if td, ok := l.exprTypes[fn.Obj]; ok {
                                if td.Kind == check.TStr {
                                        if sym, found := yiltruntime.StringMethodMapping[fn.Field]; found {
                                                fnName = sym
                                                methodReceiver = l.expr(fn.Obj)
                                        }
                                } else if td.Kind == check.TTable {
                                        if sym, found := yiltruntime.TableMethodMapping[fn.Field]; found {
                                                fnName = sym
                                                methodReceiver = l.expr(fn.Obj)
                                        }
                                } else if td.Kind == check.TGen {
                                        // Gen-typed receivers (e.g. values retrieved from
                                        // tables via indexing, or function parameters without
                                        // type annotations) should still resolve method calls
                                        // to runtime functions. Try string methods first,
                                        // then table methods. At runtime, the tagged value's
                                        // tag byte determines which path is taken.
                                        if sym, found := yiltruntime.StringMethodMapping[fn.Field]; found {
                                                fnName = sym
                                                methodReceiver = l.expr(fn.Obj)
                                        } else if sym, found := yiltruntime.TableMethodMapping[fn.Field]; found {
                                                fnName = sym
                                                methodReceiver = l.expr(fn.Obj)
                                        }
                                } else if td.Kind == check.TStruct {
                                        // Struct method call: p.distance(other) → Point_distance(p, other)
                                        // Methods are detected by convention: "StructName_methodname".
                                        // We check if the function "StructName_field" exists in userFuncs.
                                        methodFnName := td.Name + "_" + fn.Field
                                        if l.userFuncs[methodFnName] {
                                                fnName = methodFnName
                                                methodReceiver = l.expr(fn.Obj)
                                        }
                                }
                        }
                        // Fall through to module function call if not resolved as value method
                        if fnName == "" {
                                if obj, ok := fn.Obj.(*ast.Ident); ok {
                                        // Check if the module name is actually a local variable
                                        // holding a function value with a member field access.
                                        if _, isLocal := l.locals[obj.Name]; isLocal {
                                                indirectCallee = l.expr(e.Func)
                                        } else if sym, found := yiltruntime.ResolveModuleFunction(obj.Name, fn.Field); found {
                                                fnName = sym
                                        } else if l.importAliases != nil {
                                                if origModule, ok := l.importAliases[obj.Name]; ok {
                                                        if sym, found := yiltruntime.ResolveModuleFunction(origModule, fn.Field); found {
                                                                fnName = sym
                                                        } else {
                                                                fnName = origModule + "." + fn.Field
                                                        }
                                                } else {
                                                        fnName = obj.Name + "." + fn.Field
                                                }
                                        } else {
                                                fnName = obj.Name + "." + fn.Field
                                        }
                                }
                        }
                default:
                        // The callee is an arbitrary expression (e.g., another call
                        // like compose(f, g)(x), or any expression that evaluates to
                        // a function value). Use an indirect call.
                        indirectCallee = l.expr(e.Func)
                }
                args := make([]*ir.Value, 0, len(e.Args)+1)
                if methodReceiver != nil {
                        args = append(args, methodReceiver)
                }
                for _, a := range e.Args {
                        args = append(args, l.expr(a))
                }

                // Inject default parameter values for any missing trailing arguments.
                // Default params are always at the end, so if we have fewer args
                // than the function's total param count, fill in from defaults.
                if defaults, hasDefaults := l.defaultParams[fnName]; hasDefaults && fnName != "" {
                        totalParams := len(defaults)
                        // Adjust for method receiver: if methodReceiver was prepended,
                        // the user-provided args count is len(e.Args), but the function's
                        // first param is the receiver.  For struct methods, the receiver
                        // is NOT in the AST params (it's prepended at call site).
                        paramOffset := 0
                        if methodReceiver != nil {
                                // The receiver is arg[0], user args start at arg[1].
                                // The function's first declared param is the receiver,
                                // so the default-checking should start at param index 1.
                                paramOffset = 1
                        }
                        for len(args) < totalParams+paramOffset {
                                idx := len(args) - paramOffset
                                if idx >= 0 && idx < len(defaults) && defaults[idx] != nil {
                                        args = append(args, l.expr(defaults[idx]))
                                } else {
                                        break
                                }
                        }
                }

                // Handle variadic functions: if fnName is a variadic user function
                // and we have more args than fixed params, pack the extras into a table.
                if fixedCount, isVariadic := l.variadicFuncs[fnName]; isVariadic {
                        // If spread is used, the spread expression is already a table/array
                        if e.Spread != nil {
                                spreadVal := l.expr(*e.Spread)
                                if len(args) > fixedCount {
                                        args = args[:fixedCount]
                                }
                                args = append(args, spreadVal)
                        } else if len(args) > fixedCount {
                                extraArgs := args[fixedCount:]
                                args = args[:fixedCount]
                                // Create a table with int keys 0, 1, 2, ...
                                tbl := b.TableNew(len(extraArgs), "varargs")
                                for i, ea := range extraArgs {
                                        idx := b.ConstInt(int64(i), "vararg.idx")
                                        b.TableSet(tbl, idx, ea)
                                }
                                args = append(args, tbl)
                        } else {
                                // Exactly fixedCount args (or fewer) — pass empty table for variadic param
                                args = append(args, b.TableNew(0, "varargs.empty"))
                        }
                } else if e.Spread != nil {
                        // Spread on a non-variadic function — error already reported by checker
                        _ = l.expr(*e.Spread)
                }

                // For anonymous functions, the calling convention is:
                //   Call(anonFuncName, [closureRaw, userArgs...])
                // The anon function's first param is always __env_ptr (the closure struct).
                // Untag the closure value and prepend the raw pointer as first arg.
                if fnIdent, ok := e.Func.(*ast.Ident); ok {
                        if anonFuncName, isAnon := l.anonNames[fnIdent.Name]; isAnon {
                                closureVal := l.locals[fnIdent.Name]
                                if closureVal != nil {
                                        closureRaw := b.Untag(closureVal, ir.VRaw, "closure_raw")
                                        allArgs := make([]*ir.Value, 0, 1+len(args))
                                        allArgs = append(allArgs, closureRaw)
                                        allArgs = append(allArgs, args...)
                                        return b.Call(anonFuncName, allArgs, ir.VRaw, "closure_call")
                                }
                        }
                }

                if indirectCallee != nil {
                        // Any local variable holding a function value is a closure
                        // (either capturing or a first-class function returned from
                        // another function).  Always route through the trampoline
                        // which strips the tag, reads fn_ptr from closure[0], and
                        // tail-jumps to it.  This handles both capturing closures
                        // created in this function (not in anonCaptures) and
                        // function values returned from other functions (where the
                        // type checker may not have inferred TFunc due to concurrent
                        // body checking).
                        allArgs := make([]*ir.Value, 0, 1+len(args))
                        allArgs = append(allArgs, indirectCallee)
                        allArgs = append(allArgs, args...)
                        return b.Call("y_closure_trampoline", allArgs, ir.VRaw, "closure_tramp_call")
                }
                // For direct calls, verify the target exists. If fnName is neither a
                // user-defined function nor resolvable by runtimeSymbolName, skip the
                // call and return a placeholder. This handles built-in stubs (like
                // "compose") that the checker knows about but have no runtime impl.
                if fnName != "" {
                        _, isBuiltin := yiltruntime.ResolveBuiltin(fnName)
                        _, isRuntime := yiltruntime.Lookup(fnName)
                        if l.userFuncs[fnName] || isBuiltin || yiltruntime.IsModuleFunction(fnName) || isRuntime {
                                return b.Call(fnName, args, ir.VRaw, "call")
                        }
                        // Check if it matches the anon function pattern
                        if l.anonNames != nil {
                                for _, anon := range l.anonNames {
                                        if anon == fnName {
                                                return b.Call(fnName, args, ir.VRaw, "call")
                                        }
                                }
                        }
                }
                // Unknown function or unimplemented builtin — return placeholder
                return b.ConstInt(0, "unimpl_call")

        case *ast.IndexExpr:
                obj := l.expr(e.Obj)
                key := l.expr(e.Key)
                return b.Call("y_tab_get", []*ir.Value{obj, key}, ir.VRaw, "index")

        case *ast.MemberExpr:
                // Check if this is a module property access (sys.args, sys.env, etc.)
                // These are zero-argument runtime calls, not field accesses.
                if ident, ok := e.Obj.(*ast.Ident); ok {
                        if sym, found := yiltruntime.ResolveModuleFunction(ident.Name, e.Field); found {
                                return b.Call(sym, nil, ir.VRaw, "module."+e.Field)
                        }
                        // Resolve module aliases: 'use sys as s' → try original module name
                        if l.importAliases != nil {
                                if origModule, ok := l.importAliases[ident.Name]; ok {
                                        if sym, found := yiltruntime.ResolveModuleFunction(origModule, e.Field); found {
                                                return b.Call(sym, nil, ir.VRaw, origModule+"."+e.Field)
                                        }
                                }
                        }
                }
                obj := l.expr(e.Obj)
                // Struct field access: use table operations (structs are backed by tables)
                if objType, ok := l.exprTypes[e.Obj]; ok && objType.Kind == check.TStruct {
                        fieldKey := b.ConstStr(e.Field, "field."+e.Field)
                        fieldLen := b.ConstRawInt(int64(len(e.Field)), "field."+e.Field+".len")
                        fieldStr := b.Call("y_str_new", []*ir.Value{fieldKey, fieldLen}, ir.VRaw, "field."+e.Field+".str")
                        return b.TableGet(obj, fieldStr, "struct."+e.Field)
                }
                // For non-struct types: if the object is a known local variable, use
                // table-based field access. Otherwise use MemberGet for module-level access.
                if ident, ok := e.Obj.(*ast.Ident); ok {
                        if _, isLocal := l.locals[ident.Name]; !isLocal {
                                return b.MemberGet(obj, e.Field, "member")
                        }
                }
                // Generic table member access: field access on a table variable
                // uses table_get with the field name as a string key.
                fieldKey := b.ConstStr(e.Field, "member."+e.Field)
                fieldLen := b.ConstRawInt(int64(len(e.Field)), "member."+e.Field+".len")
                fieldStr := b.Call("y_str_new", []*ir.Value{fieldKey, fieldLen}, ir.VRaw, "member."+e.Field+".str")
                return b.TableGet(obj, fieldStr, "member.get."+e.Field)

        case *ast.TableLit:
                tab := b.TableNew(len(e.Entries), "table")
                for _, entry := range e.Entries {
                        key := l.expr(entry.Key)
                        val := l.expr(entry.Value)
                        b.TableSet(tab, key, val)
                }
                return tab

        case *ast.StructLit:
                // Structs are represented as tables internally: field_name -> value
                tab := b.TableNew(len(e.Fields), "struct_"+e.Name)
                for _, finit := range e.Fields {
                        fieldKey := b.ConstStr(finit.Name, "field."+finit.Name)
                        fieldLen := b.ConstRawInt(int64(len(finit.Name)), "field."+finit.Name+".len")
                        fieldStr := b.Call("y_str_new", []*ir.Value{fieldKey, fieldLen}, ir.VRaw, "field."+finit.Name+".str")
                        val := l.expr(finit.Value)
                        b.TableSet(tab, fieldStr, val)
                }
                return tab

        case *ast.TupleExpr:
                // Tuples are represented as tables with integer keys: 0, 1, 2, ...
                tab := b.TableNew(len(e.Elts), "tuple")
                for i, elt := range e.Elts {
                        key := b.ConstInt(int64(i), "tuple.idx")
                        val := l.expr(elt)
                        b.TableSet(tab, key, val)
                }
                return tab

        case *ast.EnumLit:
                // Enums are represented as tables with a "_v" key holding the
                // variant index (integer) and an optional "_p" key for the payload.
                // Simple: Color.Red  → table{"_v": 0}
                // Payload: Ok(42)    → table{"_v": 1, "_p": 42}
                ei, ok := l.enumInfos[e.EnumName]
                if !ok {
                        return b.ConstNil("enum.unknown")
                }
                variantIdx, ok := ei.VariantIndex[e.VariantName]
                if !ok {
                        return b.ConstNil("enum.unknown_variant")
                }
                vi := ei.Variants[variantIdx]

                // Determine table size: 1 for variant index, +1 if payload
                size := 1
                if vi.Payload != nil {
                        size = 2
                }
                tab := b.TableNew(size, "enum."+e.EnumName)

                // Set variant index key "_v"
                vKey := l.tableStrKey(b, "_v")
                b.TableSet(tab, vKey, b.ConstInt(int64(variantIdx), "enum._v"))

                // Set payload key "_p" if variant has a payload
                if vi.Payload != nil && e.Payload != nil {
                        pKey := l.tableStrKey(b, "_p")
                        pVal := l.expr(e.Payload)
                        b.TableSet(tab, pKey, pVal)
                }

                return tab

        case *ast.AssignExpr:
                val := l.expr(e.Value)
                if id, ok := e.Target.(*ast.Ident); ok {
                        if orig, ok := l.mutables[id.Name]; ok {
                                // Mutable variable: copy the new value into the
                                // original variable's slot so that all existing
                                // references (e.g. while-loop conditions) see the
                                // updated value.
                                b.Copy(val, orig)
                        } else {
                                l.locals[id.Name] = val
                        }
                }
                return val

        case *ast.IndexAssignExpr:
                obj := l.expr(e.Obj)
                key := l.expr(e.Key)
                val := l.expr(e.Value)
                b.Call("y_tab_set", []*ir.Value{obj, key, val}, ir.VRaw, "")
                return val

        case *ast.MemberAssignExpr:
                obj := l.expr(e.Obj)
                val := l.expr(e.Value)
                // Member assignment: obj.field = value  →  table_set(obj, "field", value)
                fieldKey := b.ConstStr(e.Field, "member.key")
                fieldLen := b.ConstRawInt(int64(len(e.Field)), "member.key.len")
                fieldStr := b.Call("y_str_new", []*ir.Value{fieldKey, fieldLen}, ir.VRaw, "member.str")
                b.TableSet(obj, fieldStr, val)
                return val

        case *ast.ErrorPropExpr:
                val := l.expr(e.Expr)
                errCheck := b.Call("y_is_error", []*ir.Value{val}, ir.VRaw, "err.check")
                l.blockID++
                errBlock := l.fn.AddBlock(fmt.Sprintf("err.prop%d", l.blockID))
                contBlock := l.fn.AddBlock(fmt.Sprintf("err.prop.cont%d", l.blockID))
                b.Branch(errCheck, errBlock, contBlock, nil, nil)
                b.SetBlock(errBlock)
                b.Return(val)
                b.SetBlock(contBlock)
                return val

        case *ast.SpawnExpr:
                // Synchronous stub: spawn calls the function directly and returns the result.
                // A real implementation would create a thread/task; for now we just call.
                return l.expr(e.Call)

        case *ast.AwaitExpr:
                // Synchronous stub: await is a no-op since spawn already ran synchronously.
                return l.expr(e.Handle)

        case *ast.FnExpr:
                // Anonymous function used as an expression (e.g., passed as argument,
                // or invoked immediately like (fn(x){x})(42)).
                // Create a separate IR function and return a proper closure value.
                // All closures (capturing and non-capturing) are allocated as closure
                // structs so they can be called uniformly through the trampoline.
                anonName := fmt.Sprintf("%s.__anon_%d", l.curFnName, l.anonCount)
                l.anonCount++
                captures := l.lowerAnonFn(e, anonName, "")

                // Sync the main builder's register counter with the module's.
                // lowerAnonFn creates an anon builder that advances module.nextRegID;
                // without this sync, the main builder would reuse stale IDs.
                b.SyncFromModule()

                // Always allocate a closure struct (even for non-capturing closures)
                // so the tagged value carries a valid fn_ptr. This is required when
                // the closure is passed as an argument or called across function
                // boundaries where the caller doesn't have anonNames.
                // Layout: [fn_ptr, n_captures, capture_0, capture_1, ...]
                nCap := len(captures)
                nCapVal := b.ConstRawInt(int64(nCap), "n_cap")
                closurePtr := b.Call("y_closure_new", []*ir.Value{nCapVal}, ir.VRaw, "closure_ptr")

                // Store fn_ptr at index 0 via linker relocation.
                b.Call("y_closure_set_fnptr_"+anonName, []*ir.Value{closurePtr}, ir.VVoid, "set_fnptr")

                // Store n_captures at index 1
                nCapIdx := b.ConstRawInt(1, "idx_1")
                b.Call("y_closure_set", []*ir.Value{closurePtr, nCapIdx, nCapVal}, ir.VVoid, "set_ncap")

                // Store each captured value at index 2+i
                for i, capName := range captures {
                        if capVal, ok := l.locals[capName]; ok {
                                idxVal := b.ConstRawInt(int64(2+i), fmt.Sprintf("idx_%d", 2+i))
                                b.Call("y_closure_set", []*ir.Value{closurePtr, idxVal, capVal}, ir.VVoid, fmt.Sprintf("set_cap_%s", capName))
                        }
                }

                // Tag closure pointer as TAG_FUNC
                closureTagged := b.Tag(closurePtr, ir.TagFunc, "closure_tagged")
                return closureTagged

        }

        return b.ConstInt(0, "")
}

func (l *lowerer) shortCircuitAnd(left *ir.Value, right ast.Expr) *ir.Value {
        b := l.builder
        l.blockID++
        rightBlock := l.fn.AddBlock(fmt.Sprintf("and.right%d", l.blockID))
        mergeBlock := l.fn.AddBlock(fmt.Sprintf("and.merge%d", l.blockID))

        // Create a non-constant slot for the result.  We use Or(val, 0) to
        // force a stack slot (Or is identity after tag stripping).  Mark NoFold
        // so the constant folder doesn't destroy it when both operands are const.
        // Initially the slot holds the left operand's value.
        zero := b.ConstInt(0, "")
        result := b.Or(left, zero, "and.result")
        b.CurBlock().MarkNoFold()

        // Short-circuit: if left is FALSE (zero payload), skip right and
        // return left (which is false).  If left is TRUE (non-zero), evaluate
        // right.
        //
        // Previously this used b.Not(left) which is a BITWISE NOT — broken
        // for tagged bools because it corrupts the tag bits.  Instead, we
        // branch directly on left: Branch takes the "then" branch when the
        // condition is non-zero (truthy).  For `and`, we want to go to
        // rightBlock when left is TRUE, and mergeBlock when left is FALSE.
        b.Branch(left, rightBlock, mergeBlock, nil, nil)

        // Evaluate right in the right branch and overwrite the result slot.
        b.SetBlock(rightBlock)
        rightResult := l.expr(right)
        b.Copy(rightResult, result)
        b.Jump(mergeBlock, nil)

        b.SetBlock(mergeBlock)
        // The result SSA value still points to the same slot, which now holds
        // whichever value was last written (left in merge-from-entry, right
        // in merge-from-right).
        return result
}

// lowerInterpStr lowers an f-string interpolation to a series of
// y_str_new / to_str / y_str_concat calls that build the result string.
func (l *lowerer) lowerInterpStr(e *ast.InterpStr, b *ir.Builder) *ir.Value {
        if len(e.Parts) == 0 {
                // Empty f-string f"" → create an empty string.
                strVal := b.ConstStr("", "str.data")
                lenVal := b.ConstRawInt(0, "str.len")
                return b.Call("y_str_new", []*ir.Value{strVal, lenVal}, ir.VRaw, "str")
        }

        var result *ir.Value
        for _, part := range e.Parts {
                var partVal *ir.Value
                if part.Expr != nil {
                        // Expression part: evaluate, then convert to string via to_str.
                        raw := l.expr(part.Expr)
                        partVal = b.Call("to_str", []*ir.Value{raw}, ir.VRaw, "to_str")
                } else {
                        // Literal string part.
                        strVal := b.ConstStr(part.Lit, "str.data")
                        lenVal := b.ConstRawInt(int64(len(part.Lit)), "str.len")
                        partVal = b.Call("y_str_new", []*ir.Value{strVal, lenVal}, ir.VRaw, "str")
                }

                if result == nil {
                        result = partVal
                } else {
                        result = b.Call("y_str_concat", []*ir.Value{result, partVal}, ir.VRaw, "strcat")
                }
        }

        if result == nil {
                strVal := b.ConstStr("", "str.data")
                lenVal := b.ConstRawInt(0, "str.len")
                result = b.Call("y_str_new", []*ir.Value{strVal, lenVal}, ir.VRaw, "str")
        }
        return result
}

// findFreeVars walks the AST of an anonymous function body and returns the set
// of variable names that are referenced but not defined within the function itself.
// paramNames are the function's own parameters (which are "defined" inside).
// outerNames are the variable names available in the enclosing scope.
func findFreeVars(body []ast.Stmt, paramNames map[string]bool, outerNames map[string]bool) []string {
        free := make(map[string]bool)
        var walkExpr func(e ast.Expr)
        var walkStmt func(s ast.Stmt)

        walkExpr = func(e ast.Expr) {
                switch n := e.(type) {
                case *ast.Ident:
                        if !paramNames[n.Name] && outerNames[n.Name] {
                                free[n.Name] = true
                        }
                case *ast.BinOp:
                        walkExpr(n.Left)
                        walkExpr(n.Right)
                case *ast.UnaryOp:
                        walkExpr(n.Operand)
                case *ast.CallExpr:
                        walkExpr(n.Func)
                        for _, a := range n.Args {
                                walkExpr(a)
                        }
                case *ast.IndexExpr:
                        walkExpr(n.Obj)
                        walkExpr(n.Key)
                case *ast.MemberExpr:
                        walkExpr(n.Obj)
                case *ast.AssignExpr:
                        walkExpr(n.Target)
                        walkExpr(n.Value)
                case *ast.IfExpr:
                        for _, br := range n.Branches {
                                walkExpr(br.Cond)
                                for _, s := range br.Body {
                                        walkStmt(s)
                                }
                        }
                case *ast.InterpStr:
                        for _, p := range n.Parts {
                                if p.Expr != nil {
                                        walkExpr(p.Expr)
                                }
                        }
                case *ast.FnExpr:
                        // Nested function: don't recurse into its body for free var
                        // detection — the inner function's captures are its own concern.
                        // But we do need to check if the inner function references
                        // our parameters (which would make it a nested closure).
                case *ast.SpawnExpr:
                        walkExpr(n.Call)
                case *ast.AwaitExpr:
                        walkExpr(n.Handle)
                case *ast.IncrDecrExpr:
                        walkExpr(n.Operand)
                case *ast.TupleExpr:
                        for _, elt := range n.Elts {
                                walkExpr(elt)
                        }
                case *ast.EnumLit:
                        if n.Payload != nil {
                                walkExpr(n.Payload)
                        }
                case *ast.MatchExpr:
                        walkExpr(n.Subject)
                        for _, c := range n.Cases {
                                if c.Value != nil {
                                        walkExpr(c.Value)
                                }
                                for _, s := range c.Body {
                                        walkStmt(s)
                                }
                        }
                        for _, s := range n.Default {
                                walkStmt(s)
                        }
                case *ast.IndexAssignExpr:
                        walkExpr(n.Obj)
                        walkExpr(n.Key)
                        walkExpr(n.Value)
                case *ast.MemberAssignExpr:
                        walkExpr(n.Obj)
                        walkExpr(n.Value)
                case *ast.ErrorPropExpr:
                        walkExpr(n.Expr)
                }
        }

        walkStmt = func(s ast.Stmt) {
                switch n := s.(type) {
                case *ast.LetStmt:
                        // The variable name is defined here, so it's not free.
                        // But we still need to walk the initializer expression.
                        innerParams := copyMap(paramNames)
                        innerParams[n.Name] = true
                        walkExpr(n.Value)
                case *ast.TupleDestructStmt:
                        innerParams := copyMap(paramNames)
                        for _, name := range n.Names {
                                innerParams[name] = true
                        }
                        walkExpr(n.Value)
                case *ast.ExprStmt:
                        walkExpr(n.Expr)
                case *ast.ReturnStmt:
                        if n.Value != nil {
                                walkExpr(n.Value)
                        }
                case *ast.IfStmt:
                        for _, br := range n.Branches {
                                walkExpr(br.Cond)
                                for _, s := range br.Body {
                                        walkStmt(s)
                                }
                        }
                        for _, s := range n.Else {
                                walkStmt(s)
                        }
                case *ast.WhileStmt:
                        walkExpr(n.Cond)
                        for _, s := range n.Body {
                                walkStmt(s)
                        }
                case *ast.ForStmt:
                        walkExpr(n.Over)
                        for _, s := range n.Body {
                                walkStmt(s)
                        }
                case *ast.MatchStmt:
                        walkExpr(n.Subject)
                        for _, c := range n.Cases {
                                if c.Value != nil {
                                        walkExpr(c.Value)
                                }
                                for _, s := range c.Body {
                                        walkStmt(s)
                                }
                        }
                        for _, s := range n.Default {
                                walkStmt(s)
                        }
                case *ast.BreakStmt, *ast.ContinueStmt:
                        // no expressions
                case *ast.AssertStmt:
                        walkExpr(n.Cond)
                        if n.Message != nil {
                                walkExpr(n.Message)
                        }
                }
        }

        for _, s := range body {
                walkStmt(s)
        }

        // Return sorted list for determinism
        result := make([]string, 0, len(free))
        for name := range free {
                result = append(result, name)
        }
        sort.Strings(result)
        return result
}

func copyMap(m map[string]bool) map[string]bool {
        r := make(map[string]bool, len(m))
        for k, v := range m {
                r[k] = v
        }
        return r
}

// lowerAnonFn lowers an anonymous function expression into a separate IR function.
// If the function captures variables from the enclosing scope, those variables are
// added as extra parameters after the user-visible parameters.
// Returns the list of captured variable names (empty for non-capturing functions).
func (l *lowerer) lowerAnonFn(fnExpr *ast.FnExpr, name string, bindingName string) []string {
        // Build set of outer-scope variable names (all locals in the enclosing function)
        outerNames := make(map[string]bool)
        for v := range l.locals {
                outerNames[v] = true
        }

        // Build set of own parameter names
        paramNames := make(map[string]bool)
        for _, p := range fnExpr.Params {
                paramNames[p.Name] = true
        }

        // Find free variables (captured from outer scope)
        captures := findFreeVars(fnExpr.Body, paramNames, outerNames)

        // Determine return type
        retType := ir.VVoid
        if fnExpr.ReturnType != nil {
                retType = ir.VRaw
        } else if td, ok := l.exprTypes[fnExpr]; ok && td.Kind == check.TFunc && td.Ret != nil && td.Ret.Kind != check.TVoid {
                // Checker inferred a non-void return type for this closure.
                retType = ir.VRaw
        }

        // Create the function in the module.
        // The first parameter is always __env_ptr (the closure struct pointer),
        // even for non-capturing closures. This is needed because all closures
        // are now allocated as closure structs and called through the trampoline
        // (which always passes the closure ptr as the first argument in RDI).
        // For non-capturing closures, __env_ptr is simply unused.
        f := l.module.AddFunc(name, retType, false, false)
        envParam := f.NewParam(l.module.AllocID(), ir.VRaw, "__env_ptr")
        for _, p := range fnExpr.Params {
                f.NewParam(l.module.AllocID(), ir.VRaw, p.Name)
        }

        // Create a temporary lowerer for the anonymous function body
        anonBuilder := ir.NewBuilder(l.module, f)
        anonLower := &lowerer{
                builder:      anonBuilder,
                fn:           f,
                locals:       make(map[string]*ir.Value),
                exprTypes:    l.exprTypes,
                module:       l.module,
                anonNames:    make(map[string]string),
                anonCaptures: make(map[string]*closureInfo),
                curFnName:    name,
                blockID:      0,
                userFuncs:    l.userFuncs,
        }

        // Allow the anonymous function to call itself recursively via its binding name.
        if bindingName != "" {
                anonLower.anonNames[bindingName] = name
        }

        // Bind parameters as locals in the anonymous function.
        // Params start at index 1 (index 0 is __env_ptr).
        for i, p := range fnExpr.Params {
                anonLower.locals[p.Name] = f.Params[1+i]
        }

        // Bind captured variables: load from closure struct via y_closure_get(env_ptr, 2+i).
        for i, capName := range captures {
                idxVal := anonBuilder.ConstRawInt(int64(2+i), fmt.Sprintf("cap_idx_%d", i))
                capVal := anonBuilder.Call("y_closure_get", []*ir.Value{envParam, idxVal}, ir.VRaw, fmt.Sprintf("cap_%s", capName))
                anonLower.locals[capName] = capVal
                // If the captured variable was mutable in the outer scope, it's still
                // mutable here — but it's captured by value. For now, we accept this
                // limitation (P1-future: heap boxing for mutable captures).
        }

        // Lower the body
        for _, s := range fnExpr.Body {
                anonLower.stmt(s)
        }

        // Ensure terminator
        if anonBuilder.CurBlock() != nil && !anonBuilder.CurBlock().IsTerminated() {
                anonBuilder.Return(nil)
        }

        return captures
}

func (l *lowerer) shortCircuitOr(left *ir.Value, right ast.Expr) *ir.Value {
        b := l.builder
        l.blockID++
        rightBlock := l.fn.AddBlock(fmt.Sprintf("or.right%d", l.blockID))
        mergeBlock := l.fn.AddBlock(fmt.Sprintf("or.merge%d", l.blockID))

        // Same Or(val, 0) + NoFold trick for a non-constant result slot.
        zero := b.ConstInt(0, "")
        result := b.Or(left, zero, "or.result")
        b.CurBlock().MarkNoFold()

        // Check if left is true - if so, skip right evaluation
        b.Branch(left, mergeBlock, rightBlock, nil, nil)

        b.SetBlock(rightBlock)
        rightResult := l.expr(right)
        b.Copy(rightResult, result)
        b.Jump(mergeBlock, nil)

        b.SetBlock(mergeBlock)
        return result
}

// ========== Code Generation (dispatch to backends) ==========

func generateCode(fn *ir.Func, tgt target.Target) funcResult {
        switch tgt.Arch {
        case "x86_64":
                code, relocs := emitX86_64(fn)
                return funcResult{name: fn.Name, code: code, relocations: relocs}
        case "aarch64":
                return funcResult{name: fn.Name, code: emitAArch64(fn)}
        case "rv64":
                return funcResult{name: fn.Name, code: emitRV64(fn)}
        case "rv32":
                return funcResult{name: fn.Name, code: emitRV32(fn)}
        case "wasm":
                return funcResult{name: fn.Name, code: emitWASM(fn)}
        default:
                return funcResult{name: fn.Name, code: nil}
        }
}

// emitX86_64 generates x86-64 machine code for a function.
//
// It walks each basic block in the IR function, translating every IR instruction
// to the corresponding x86_64 machine code using the backend assembler.
//
// Calling convention: System V AMD64 (Linux/macOS).
//   Integer args:  RDI, RSI, RDX, RCX, R8, R9
//   Return value:  RAX
//   Callee-saved:  RBX, RBP, R12-R15
//   Arena pointer: R15
//
// Frame layout (RBP-based):
//   [RBP]      saved RBP
//   [RBP-8]    saved R15  (arena)
//   [RBP-16]   saved RBX
//   [RBP-24]   local slot 0  (parameter 0)
//   [RBP-32]   local slot 1  (parameter 1)
//   ...
//   [RBP-24-N*8]  local slot N
func emitX86_64(fn *ir.Func) ([]byte, []pendingReloc) {
        if fn.Extern || len(fn.Blocks) == 0 {
                return nil, nil
        }
        var relocs []pendingReloc

        // ---------- assign stack slots ----------
        //
        // Every SSA value that is defined (instr.Dest != nil) gets a stack slot.
        // Constants are inlined and don't need slots.

        slots := make(map[int]int) // ir.Value.ID → slot index
        nextSlot := 0

        // Parameters first.
        for i, p := range fn.Params {
                slots[p.ID] = i
                nextSlot = i + 1
        }

        // Pre-scan: assign slots for every defined value so that the
        // frame size is known before we emit code.
        for _, blk := range fn.Blocks {
                for _, ins := range blk.Instrs {
                        if ins.Dest == nil {
                                continue
                        }
                        if _, ok := slots[ins.Dest.ID]; ok {
                                continue
                        }
                        // Constants don't need spill slots (inlined at point of use).
                        // Exception: ConstStr values need slots because they resolve to
                        // actual addresses at link time via LEA+reloc.
                        // Exception: ConstRawInt values need slots so they can be loaded
                        // as raw (untagged) values instead of going through emitConst
                        // which would add a tag byte.
                        if ins.Dest.IsConst() && ins.Op != ir.OpConstStr && ins.Op != ir.OpConstRawInt {
                                continue
                        }
                        slots[ins.Dest.ID] = nextSlot
                nextSlot++
                }
        }

        // ---------- frame layout ----------
        //
        // calleePush = 8 (RBP) + 8 (R15) + 8 (RBX) = 24 bytes.
        // Local area starts at RBP-24.
        // slotOffset(k) = -(24 + k*8)

        const calleePush = 24
        localBytes := nextSlot * 8
        // Align so that (calleePush + localBytes) is a multiple of 16.
        // After a CALL pushes 8 bytes (return address), RSP must be 16-aligned,
        // i.e. (calleePush + localBytes + 8) % 16 == 0
        // ⟹ (calleePush + localBytes) % 16 == 8
        // If it's already 8 mod 16, fine; otherwise round up.
        totalInner := calleePush + localBytes
        if totalInner%16 != 8 {
                localBytes += 16 - ((totalInner + 8) % 16)
        }

        slotOff := func(k int) int32 {
                return -int32(calleePush + k*8)
        }

        // ---------- assemble ----------
        a := x86_64.NewAsm()

        // --- prologue ---
        a.PUSH(x86_64.RBP)           // push rbp
        a.MovRR(x86_64.RBP, x86_64.RSP) // mov rbp, rsp
        a.PUSH(x86_64.R15)           // push r15  (arena pointer)
        a.PUSH(x86_64.RBX)           // push rbx
        if localBytes > 0 {
                a.SubRI(x86_64.RSP, int64(localBytes)) // sub rsp, localBytes
        }

        // Store incoming ABI argument registers to their parameter slots.
        sysvArgs := []x86_64.Reg{
                x86_64.RDI, x86_64.RSI, x86_64.RDX,
                x86_64.RCX, x86_64.R8, x86_64.R9,
        }
        for i := range fn.Params {
                if i < len(sysvArgs) {
                        a.MovRMMem(x86_64.MemBase(x86_64.RBP, slotOff(i)), sysvArgs[i])
                }
        }

        // Helper: load a value from its slot (or inline a constant) into dst.
        loadVal := func(v *ir.Value, dst x86_64.Reg) {
                // ConstStr values are resolved to actual addresses at link time
                // and stored in stack slots by the OpConstStr handler. Always
                // load them from their slot, never inline as 0.
                if v.IsConst() && v.Const != nil && v.Const.Kind == ir.VStr {
                        if s, ok := slots[v.ID]; ok {
                                a.MovMemR(dst, x86_64.MemBase(x86_64.RBP, slotOff(s)))
                                return
                        }
                        // No slot (shouldn't happen) — emit 0 as fallback.
                        a.MovZeroR64(dst)
                        return
                }
                // ConstRawInt values are stored as raw (untagged) integers in
                // their stack slots.  Load from slot to avoid emitConst which
                // would add a tag byte.
                if v.IsConst() && v.Const != nil && v.Type == ir.VRaw && v.Const.Kind == ir.VInt {
                        if s, ok := slots[v.ID]; ok {
                                a.MovMemR(dst, x86_64.MemBase(x86_64.RBP, slotOff(s)))
                                return
                        }
                        // No slot — load the raw value directly (no tag).
                        loadValConst(a, v.Const.IntVal, dst)
                        return
                }
                if v.IsConst() {
                        emitConst(a, v, dst)
                        return
                }
                if s, ok := slots[v.ID]; ok {
                        a.MovMemR(dst, x86_64.MemBase(x86_64.RBP, slotOff(s)))
                        return
                }
                // No slot → treat as zero (shouldn't happen in well-formed IR).
                a.MovZeroR64(dst)
        }

        // Helper: store reg to v's slot.
        storeVal := func(v *ir.Value, src x86_64.Reg) {
                if s, ok := slots[v.ID]; ok {
                        a.MovRMMem(x86_64.MemBase(x86_64.RBP, slotOff(s)), src)
                }
        }

        // Helper: ensure return value for a call is captured.
        // After CALL, RAX holds the return value.
        captureRet := func(dest *ir.Value) {
                if dest != nil {
                        storeVal(dest, x86_64.RAX)
                }
        }

        // --- walk blocks ---
        for _, blk := range fn.Blocks {
                // Emit a label for every non-entry block.
                if blk != fn.Entry {
                        a.Label(blk.Label)
                }

                for _, ins := range blk.Instrs {
                        switch ins.Op {

                        // ================ constants ================
                        case ir.OpConstInt, ir.OpConstUint:
                                if ins.Dest != nil {
                                        emitConst(a, ins.Dest, x86_64.RAX)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpConstFp:
                                if ins.Dest != nil && ins.Dest.Const != nil {
                                        bits := math.Float64bits(ins.Dest.Const.FpVal)
                                        a.MovRM64(x86_64.RAX, int64(bits))
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpConstBool:
                                if ins.Dest != nil && ins.Dest.Const != nil {
                                        if ins.Dest.Const.BoolVal {
                                                a.MovRM(x86_64.RAX, 1)
                                        } else {
                                                a.MovZeroR64(x86_64.RAX)
                                        }
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpConstStr:
                                // Embed string data in the binary's data section and
                                // emit a LEA RAX, [RIP+disp] with a linker relocation
                                // to resolve the string's actual address at link time.
                                if ins.Dest != nil && ins.Dest.Const != nil && ins.Dest.Const.StrVal != "" {
                                        stringDataMu.Lock()
                                        symName := fmt.Sprintf(".yilt.str.%d", stringDataSeq)
                                        stringDataSeq++
                                        stringDataMap[symName] = ins.Dest.Const.StrVal
                                        stringDataMu.Unlock()

                                        // lea rax, [rip + 0]  — REX.W LEA, ModR/M=05 (RIP-relative)
                                        // 48 8d 05 xx xx xx xx (7 bytes)
                                        leaOffset := uint64(a.Offset())
                                        a.EmitBytes([]byte{0x48, 0x8d, 0x05, 0x00, 0x00, 0x00, 0x00})
                                        relocs = append(relocs, pendingReloc{
                                                offset: leaOffset + 3, // 4-byte displacement starts at byte 3
                                                target: symName,
                                        })
                                        storeVal(ins.Dest, x86_64.RAX)
                                } else {
                                        a.MovZeroR64(x86_64.RAX)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpConstNil:
                                if ins.Dest != nil {
                                        a.MovZeroR64(x86_64.RAX)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpConstRawInt:
                                // Raw (untagged) integer — no tag in upper bits.
                                // Used for passing raw sizes/lengths to runtime functions.
                                if ins.Dest != nil && ins.Dest.Const != nil {
                                        loadValConst(a, ins.Dest.Const.IntVal, x86_64.RAX)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        // ================ arithmetic ================
                        //
                        // All integer arithmetic must handle NaN-boxed tagged values.
                        // Tag layout: bits 63-56 = tag, bits 55-0 = payload.
                        // For tagged int (tag=1): 0x01XXXXXXXXXXXXXX
                        //
                        // Strategy: strip tags from both operands (AND with mask),
                        // perform the operation on raw payloads, then OR the tag
                        // back on the result. This avoids tag corruption from
                        // carries/borrows crossing the tag/payload boundary.
                        //
                        // For ADD: tag_a + tag_a = 2*tag_a which corrupts the result.
                        // For SUB: tag_a - tag_a = 0 which strips the tag.
                        // For MUL: tag_a * tag_a corrupts the result.
                        // For DIV/MOD: CQO extends sign bit into RDX, which is
                        //   wrong if tag is present.

                        case ir.OpAdd:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.AddRR(x86_64.RAX, x86_64.RCX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpSub:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.SubRR(x86_64.RAX, x86_64.RCX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpMul:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.IMul2RR(x86_64.RAX, x86_64.RCX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpDiv:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.CQO()
                                        a.IDivRR(x86_64.RCX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpMod:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.CQO()
                                        a.IDivRR(x86_64.RCX)
                                        a.MovRR(x86_64.RAX, x86_64.RDX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        // ================ unary ================
                        case ir.OpNeg:
                                if len(ins.Src) >= 1 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        // Negate only the payload (bits 55-0), preserving the tag.
                                        // Extract and mask payload, negate, re-apply tag.
                                        a.MovRR(x86_64.RCX, x86_64.RAX)
                                        a.ShrRI(x86_64.RCX, 56) // RCX = tag
                                        a.MovRM64(x86_64.R11, int64(0x00FFFFFFFFFFFFFF))
                                        a.AndRR(x86_64.RAX, x86_64.R11) // RAX = payload
                                        a.NegR(x86_64.RAX)
                                        a.AndRR(x86_64.RAX, x86_64.R11) // mask result
                                        a.ShlRI(x86_64.RCX, 56)      // RCX = tag << 56
                                        a.OrRR(x86_64.RAX, x86_64.RCX)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpNot:
                                // Logical not: result = (val == 0) ? 1 : 0, tagged as boolean.
                                if len(ins.Src) >= 1 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        a.MovZeroR64(x86_64.RCX)
                                        a.CmpRR(x86_64.RAX, x86_64.RCX)
                                        // SETE al (1 if equal, 0 if not)
                                        a.SetCC(x86_64.CondE, x86_64.AL)
                                        a.MovZX8_64(x86_64.RAX, x86_64.AL)
                                        // Tag as boolean: TAG_BOOL=2 in bits 63-56.
                                        a.MovRM64(x86_64.RCX, int64(0x0200000000000000))
                                        a.OrRR(x86_64.RAX, x86_64.RCX)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpBitNot:
                                // Bitwise NOT: invert only the payload, preserving the tag.
                                if len(ins.Src) >= 1 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        a.MovRR(x86_64.RCX, x86_64.RAX)
                                        a.ShrRI(x86_64.RCX, 56) // RCX = tag
                                        a.MovRM64(x86_64.R11, int64(0x00FFFFFFFFFFFFFF))
                                        a.AndRR(x86_64.RAX, x86_64.R11) // RAX = payload
                                        a.NotR(x86_64.RAX)             // RAX = ~payload
                                        a.AndRR(x86_64.RAX, x86_64.R11) // mask result
                                        a.ShlRI(x86_64.RCX, 56)      // RCX = tag << 56
                                        a.OrRR(x86_64.RAX, x86_64.RCX)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        // ================ bitwise ================
                        case ir.OpAnd:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.AndRR(x86_64.RAX, x86_64.RCX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpOr:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.OrRR(x86_64.RAX, x86_64.RCX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpXor:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.XorRR(x86_64.RAX, x86_64.RCX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpShl:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.ShlRCL(x86_64.RAX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpShr:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadVal(ins.Src[1], x86_64.RCX)
                                        emitTaggedArith(a)
                                        a.ShrRCL(x86_64.RAX)
                                        emitRestoreTag(a)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        // ================ comparison ================
                        case ir.OpEq:
                                emitCmp(a, ins, loadVal, storeVal, x86_64.CondE, false)
                        case ir.OpNeq:
                                emitCmp(a, ins, loadVal, storeVal, x86_64.CondNE, false)
                        case ir.OpLt:
                                emitCmp(a, ins, loadVal, storeVal, x86_64.CondL, true)
                        case ir.OpLe:
                                emitCmp(a, ins, loadVal, storeVal, x86_64.CondLE, true)
                        case ir.OpGt:
                                emitCmp(a, ins, loadVal, storeVal, x86_64.CondG, true)
                        case ir.OpGe:
                                emitCmp(a, ins, loadVal, storeVal, x86_64.CondGE, true)

                        // ================ call / return ================
                        case ir.OpCall:
                                if ins.Meta == nil {
                                        break
                                }
                                fnName := ins.Meta.FnName
                                // System V: first 6 integer args in RDI,RSI,RDX,RCX,R8,R9.
                                emitCall(a, fnName, ins.Src, loadVal, &relocs)
                                captureRet(ins.Dest)

                        case ir.OpCallIndirect:
                                if len(ins.Src) >= 1 {
                                        // Src[0] = function pointer, Src[1:] = args
                                        loadVal(ins.Src[0], x86_64.R11)
                                        emitCallRegs(a, ins.Src[1:], loadVal)
                                        a.CALLReg(x86_64.R11)
                                        captureRet(ins.Dest)
                                }

                        case ir.OpReturn:
                                if len(ins.Src) > 0 && ins.Src[0] != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                } else {
                                        a.MovZeroR64(x86_64.RAX)
                                }
                                // Epilogue
                                if localBytes > 0 {
                                        a.AddRI(x86_64.RSP, int64(localBytes))
                                }
                                a.POP(x86_64.RBX)
                                a.POP(x86_64.R15)
                                a.POP(x86_64.RBP)
                                a.RET()

                        // ================ control flow ================
                        case ir.OpJump:
                                // Copy block arguments to target block parameters.
                                if ins.Meta != nil && ins.Meta.Jump != nil {
                                        emitBlockArgCopies(a, ins.Meta.Jump, slots, loadVal, storeVal)
                                }
                                if ins.Meta != nil && ins.Meta.Jump != nil {
                                        a.JMP(ins.Meta.Jump.Block.Label)
                                }

                        case ir.OpBranch:
                                if ins.Meta == nil || len(ins.Src) == 0 {
                                        break
                                }
                                cond := ins.Src[0]
                                thenBT := ins.Meta.Then
                                elseBT := ins.Meta.Else

                                // Test condition: mask off the tag byte and compare
                                // the payload against zero.  This correctly handles
                                // tagged booleans (tag=2, payload=0|1) where the full
                                // 64-bit value is always non-zero for "false".
                                loadVal(cond, x86_64.RAX)
                                a.MovRM64(x86_64.RCX, int64(0x00FFFFFFFFFFFFFF))
                                a.AndRR(x86_64.RAX, x86_64.RCX) // RAX = payload only
                                a.MovZeroR64(x86_64.RCX)
                                a.CmpRR(x86_64.RAX, x86_64.RCX)

                                // Jump to then-block if condition != 0.
                                // Use a trampoline so that we can copy args for each path
                                // without clobbering values needed by the other path.
                                thenLabel := thenBT.Block.Label
                                elseLabel := elseBT.Block.Label

                                if len(elseBT.Args) > 0 {
                                        // else has args → we need a trampoline for then.
                                        trampoline := a.GenLabel("branch_then")
                                        a.Jcc(x86_64.CondNE, trampoline)
                                        // else path: copy args and jump
                                        emitBlockArgCopies(a, elseBT, slots, loadVal, storeVal)
                                        a.JMP(elseLabel)
                                        a.Label(trampoline)
                                        emitBlockArgCopies(a, thenBT, slots, loadVal, storeVal)
                                        a.JMP(thenLabel)
                                } else if len(thenBT.Args) > 0 {
                                        // then has args, else doesn't → trampoline for else.
                                        trampoline := a.GenLabel("branch_else")
                                        a.Jcc(x86_64.CondNE, trampoline)
                                        // else path: fall through to elseLabel directly.
                                        a.JMP(elseLabel)
                                        a.Label(trampoline)
                                        emitBlockArgCopies(a, thenBT, slots, loadVal, storeVal)
                                        a.JMP(thenLabel)
                                } else {
                                        // No args on either side → simple conditional jump.
                                        a.Jcc(x86_64.CondNE, thenLabel)
                                        a.JMP(elseLabel)
                                }

                        // ================ memory ================

                        case ir.OpCopy:
                                // Copy: load Src[0] into a register, store to Dest's slot.
                                // Used for mutable variable assignments so the loop
                                // condition reads the updated value.
                                if len(ins.Src) >= 1 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpLoad:
                                // Load(addr, type, name).  addr is a pointer to a memory location.
                                if len(ins.Src) >= 1 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX) // RAX = address
                                a.MovMemR(x86_64.RAX, x86_64.MemBase(x86_64.RAX, 0)) // load [RAX]
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpStore:
                                // Store(addr, val).
                                if len(ins.Src) >= 2 {
                                        loadVal(ins.Src[1], x86_64.RCX) // RCX = value
                                        loadVal(ins.Src[0], x86_64.RAX) // RAX = address
                                        a.MovRMMem(x86_64.MemBase(x86_64.RAX, 0), x86_64.RCX) // store [RAX], RCX
                                }

                        case ir.OpAlloc:
                                // Heap allocation: call y_alloc(size).
                                if ins.Dest != nil && ins.Meta != nil {
                                        loadValConst(a, int64(ins.Meta.Size), x86_64.RDI) // first arg: size
                                        emitCall(a, "y_alloc", nil, loadVal, &relocs)
                                        captureRet(ins.Dest)
                                }

                        case ir.OpStackAlloc:
                                // Stack allocation: use the frame slot already assigned during
                                // the pre-scan. The slot holds a pointer to stack memory, but
                                // for StackAlloc we need the address OF the slot itself (not
                                // its contents). Emit lea rax, [rbp + slotOff] to compute
                                // the address and store it as the SSA value.
                                if ins.Dest != nil {
                                        if s, ok := slots[ins.Dest.ID]; ok {
                                                a.MovRR(x86_64.RAX, x86_64.RBP)
                                                // lea rax, [rbp + slotOff(s)]
                                                a.AddRI(x86_64.RAX, int64(slotOff(s)))
                                                storeVal(ins.Dest, x86_64.RAX)
                                        }
                                }

                        // ================ table ops (delegated to runtime) ================
                        case ir.OpTableNew:
                                if ins.Dest != nil && ins.Meta != nil {
                                        loadValConst(a, int64(ins.Meta.Capacity), x86_64.RDI)
                                        emitCall(a, "y_table_new", nil, loadVal, &relocs)
                                        captureRet(ins.Dest)
                                }

                        case ir.OpTableGet:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RDI)
                                        loadVal(ins.Src[1], x86_64.RSI)
                                        emitCall(a, "y_tab_get", nil, loadVal, &relocs)
                                        captureRet(ins.Dest)
                                }

                        case ir.OpTableSet:
                                if len(ins.Src) >= 3 {
                                        loadVal(ins.Src[0], x86_64.RDI)
                                        loadVal(ins.Src[1], x86_64.RSI)
                                        loadVal(ins.Src[2], x86_64.RDX)
                                        emitCall(a, "y_tab_set", nil, loadVal, &relocs)
                                }

                        case ir.OpTableLen:
                                if len(ins.Src) >= 1 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RDI)
                                        emitCall(a, "y_tab_len", nil, loadVal, &relocs)
                                        captureRet(ins.Dest)
                                }

                        case ir.OpTableDelete:
                                if len(ins.Src) >= 2 {
                                        loadVal(ins.Src[0], x86_64.RDI)
                                        loadVal(ins.Src[1], x86_64.RSI)
                                        emitCall(a, "y_tab_del", nil, loadVal, &relocs)
                                }

                        // ================ index / member ================
                        case ir.OpIndexGet:
                                if len(ins.Src) >= 2 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RDI)
                                        loadVal(ins.Src[1], x86_64.RSI)
                                        emitCall(a, "y_tab_get", nil, loadVal, &relocs)
                                        captureRet(ins.Dest)
                                }

                        case ir.OpIndexSet:
                                if len(ins.Src) >= 3 {
                                        loadVal(ins.Src[0], x86_64.RDI)
                                        loadVal(ins.Src[1], x86_64.RSI)
                                        loadVal(ins.Src[2], x86_64.RDX)
                                        emitCall(a, "y_tab_set", nil, loadVal, &relocs)
                                }

                        case ir.OpMemberGet:
                                // obj.field → call y_member_get(obj, field_ptr)
                                if len(ins.Src) >= 1 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RDI)
                                        // Field name as a pointer – for now, load a pointer to the
                                        // field name string into RSI via a simple convention.
                                        a.MovZeroR64(x86_64.RSI)
                                        emitCall(a, "y_member_get", nil, loadVal, &relocs)
                                        captureRet(ins.Dest)
                                }

                        // ================ tagged value ops ================
                        case ir.OpTag:
                                if len(ins.Src) >= 1 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        if ins.Meta != nil {
                                                // AND with value mask, OR with tag.
                                                a.AndRI(x86_64.RAX, int64(0x00FFFFFFFFFFFFFF))
                                                a.OrRI(x86_64.RAX, int64(uint64(ins.Meta.Tag)<<56))
                                        }
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpUntag:
                                if len(ins.Src) >= 1 && ins.Dest != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        a.AndRI(x86_64.RAX, int64(0x00FFFFFFFFFFFFFF))
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        case ir.OpCheckTag:
                                if len(ins.Src) >= 1 && ins.Dest != nil && ins.Meta != nil {
                                        loadVal(ins.Src[0], x86_64.RAX)
                                        loadValConst(a, int64(uint64(ins.Meta.Tag)<<56), x86_64.RCX)
                                        a.AndRR(x86_64.RAX, x86_64.RCX)
                                        a.CmpRR(x86_64.RAX, x86_64.RCX)
                                        a.SetCC(x86_64.CondE, x86_64.AL)
                                        a.MovZX8_64(x86_64.RAX, x86_64.AL)
                                        storeVal(ins.Dest, x86_64.RAX)
                                }

                        // ================ arena ================
                        case ir.OpArenaPush:
                                a.PUSH(x86_64.R15) // save arena pointer

                        case ir.OpArenaPop:
                                a.POP(x86_64.R15) // restore arena pointer

                        // ================ misc ================
                        case ir.OpNop:
                                // emit nothing

                        case ir.OpParam:
                                // Block parameter definition – handled by arg copies at
                                // jump/branch sites; nothing to emit here.

                        case ir.OpPanic:
                                a.INT3()

                        // ================ concurrency (stub) ================
                        case ir.OpSpawn:
                                // spawn is not yet implemented — emit runtime stub call
                                if ins.Dest != nil && len(ins.Src) >= 1 {
                                        loadVal(ins.Src[0], x86_64.RDI)
                                        emitCall(a, "y_spawn", nil, loadVal, &relocs)
                                        captureRet(ins.Dest)
                                }

                        case ir.OpAwait:
                                // await is not yet implemented — emit runtime stub call
                                if ins.Dest != nil && len(ins.Src) >= 1 {
                                        loadVal(ins.Src[0], x86_64.RDI)
                                        emitCall(a, "y_await", nil, loadVal, &relocs)
                                        captureRet(ins.Dest)
                                }

                        default:
                                // Unhandled opcode — trap to make bugs visible.
                                a.INT3()

                        }
                }
        }

        // --- fallback epilogue ---
        // If control falls through all blocks without a return, emit one.
        a.MovZeroR64(x86_64.RAX)
        if localBytes > 0 {
                a.AddRI(x86_64.RSP, int64(localBytes))
        }
        a.POP(x86_64.RBX)
        a.POP(x86_64.R15)
        a.POP(x86_64.RBP)
        a.RET()

        // Resolve all label fixups.
        for name := range a.Labels() {
                a.ResolveFixups(name)
        }

        return a.Bytes(), relocs
}

// emitConst materialises a compile-time constant into a register.
// Integer and boolean constants are emitted as properly tagged values
// (TagInt=1, TagBool=2) so that runtime functions like y_print can
// dispatch on the tag.
func emitConst(a *x86_64.Asm, v *ir.Value, dst x86_64.Reg) {
        if v.Const == nil {
                a.MovZeroR64(dst)
                return
        }
        switch v.Const.Kind {
        case ir.VInt:
                // Tagged integer: payload in bits 55-0, TAG_INT=1 in bits 63-56.
                // Mask to 56 bits to avoid negative int64 sign-extending into tag byte.
                payload := uint64(v.Const.IntVal) & 0x00FFFFFFFFFFFFFF
                tagged := payload | (uint64(1) << 56)
                a.MovRM64(dst, int64(tagged))
        case ir.VUint:
                // Tagged unsigned: payload in bits 55-0, TAG_INT=1 in bits 63-56.
                payload := v.Const.UintVal & 0x00FFFFFFFFFFFFFF
                tagged := payload | (uint64(1) << 56)
                a.MovRM64(dst, int64(tagged))
        case ir.VBool:
                // Tagged boolean: TAG_BOOL=2 in bits 63-56, payload 0 or 1.
                var tagged uint64
                if v.Const.BoolVal {
                        tagged = (uint64(2) << 56) | 1
                } else {
                        tagged = uint64(2) << 56
                }
                a.MovRM64(dst, int64(tagged))
        case ir.VFp:
                bits := math.Float64bits(v.Const.FpVal)
                a.MovRM64(dst, int64(bits))
        case ir.VStr:
                // String constants are represented as their raw value (0) during
                // codegen.  The actual string data is embedded in the binary by
                // y_str_new which receives the raw pointer.  At this point we
                // cannot resolve the string to an address, so we emit 0 as a
                // placeholder.  The IR lowerer should call y_str_new with the
                // string data before passing to print functions.
                a.MovZeroR64(dst)
        default:
                a.MovZeroR64(dst)
        }
}

// loadValConst loads an integer constant into dst via MOV.
func loadValConst(a *x86_64.Asm, v int64, dst x86_64.Reg) {
        if v == 0 {
                a.MovZeroR64(dst)
        } else if v >= -2147483648 && v <= 2147483647 {
                a.MovRM(dst, v)
        } else {
                a.MovRM64(dst, v)
        }
}

// emitCmp emits a comparison: dst = (lhs cc rhs) ? 1 : 0.
// The result is tagged as a boolean (TAG_BOOL=2) so that print and other
// runtime functions handle it correctly.
func emitCmp(a *x86_64.Asm, ins *ir.Instr,
        loadVal func(*ir.Value, x86_64.Reg),
        storeVal func(*ir.Value, x86_64.Reg), cc x86_64.CondCode, ordering bool) {

        if len(ins.Src) < 2 || ins.Dest == nil {
                return
        }
        loadVal(ins.Src[0], x86_64.RAX)
        loadVal(ins.Src[1], x86_64.RCX)

        if ordering {
                // For ordering comparisons (Lt, Le, Gt, Ge), we must strip
                // the tag byte and sign-extend the 56-bit payload to get
                // correct signed comparison of negative integers.
                a.PUSH(x86_64.RBX)
                a.MovRM(x86_64.RBX, int64(-1))   // RBX = 0xFFFFFFFFFFFFFFFF
                a.ShrRI(x86_64.RBX, 8)            // RBX = 0x00FFFFFFFFFFFFFF
                a.AndRR(x86_64.RAX, x86_64.RBX)  // strip tag from RAX
                a.AndRR(x86_64.RCX, x86_64.RBX)  // strip tag from RCX
                // Sign-extend bit 55 (MSB of 56-bit payload) into bits 56-63.
                a.ShlRI(x86_64.RAX, 8)
                a.SarRI(x86_64.RAX, 8)
                a.ShlRI(x86_64.RCX, 8)
                a.SarRI(x86_64.RCX, 8)
                a.POP(x86_64.RBX)
        }

        a.CmpRR(x86_64.RAX, x86_64.RCX)
        a.SetCC(cc, x86_64.AL)
        a.MovZX8_64(x86_64.RAX, x86_64.AL)
        // Tag as boolean: TAG_BOOL=2 in bits 63-56.
        a.MovRM64(x86_64.RCX, int64(0x0200000000000000))
        a.OrRR(x86_64.RAX, x86_64.RCX)
        storeVal(ins.Dest, x86_64.RAX)
}

func emitTaggedArith(a *x86_64.Asm) {
        // Save tag from RAX into R11: R11 = RAX >> 56
        a.MovRR(x86_64.R11, x86_64.RAX)
        a.ShrRI(x86_64.R11, 56)
        // Clear the tag byte from both operands.
        // Tag byte is bits 56-63. The mask 0x00FF..FF (low 56 bits) clears it.
        // We load the mask into a register and AND both operands.
        // Use RBX (callee-saved, we push/pop to preserve).
        a.PUSH(x86_64.RBX)
        // Build mask 0x00FFFFFFFFFFFFFF without large immediate:
        // MOV RBX, -1 (all 1s) then SHR RBX, 8 (clear top byte)
        a.MovRM(x86_64.RBX, int64(-1))   // RBX = 0xFFFFFFFFFFFFFFFF
        a.ShrRI(x86_64.RBX, 8)            // RBX = 0x00FFFFFFFFFFFFFF
        a.AndRR(x86_64.RAX, x86_64.RBX)  // clear tag from RAX
        a.AndRR(x86_64.RCX, x86_64.RBX)  // clear tag from RCX
        a.POP(x86_64.RBX)
}

// emitRestoreTag shifts the saved tag (from R11) to position and ORs it
// onto RAX, producing a properly tagged result.
func emitRestoreTag(a *x86_64.Asm) {
        a.ShlRI(x86_64.R11, 56) // R11 = tag << 56
        a.OrRR(x86_64.RAX, x86_64.R11)
}

func emitCall(a *x86_64.Asm, fnName string, args []*ir.Value,
        loadVal func(*ir.Value, x86_64.Reg), relocs *[]pendingReloc) {

        // Special case: y_closure_set_fnptr_X stores the code address of function X
        // into closure[0]. Instead of an actual runtime call, we emit:
        //   load args[0] (closure_ptr) into RDI
        //   LEA RAX, [RIP + X]  — resolved by linker via relocation
        //   MOV [RDI], RAX      — store fn_ptr at closure[0]
        if strings.HasPrefix(fnName, "y_closure_set_fnptr_") {
                targetFunc := strings.TrimPrefix(fnName, "y_closure_set_fnptr_")
                // Load closure_ptr into RDI (first arg)
                if len(args) > 0 {
                        loadVal(args[0], x86_64.RDI)
                }
                // LEA RAX, [RIP + 0] then add relocation for targetFunc
                leaOffset := uint64(a.Offset())
                a.LEA(x86_64.RAX, x86_64.MemDispl(0))
                if relocs != nil {
                        *relocs = append(*relocs, pendingReloc{
                                offset: leaOffset + 3, // LEA RAX, [RIP+disp] → disp is at opcode+3
                                target: targetFunc,
                        })
                }
                // MOV [RDI], RAX — store fn_ptr at closure[0]
                a.MovRMMem(x86_64.MemBase(x86_64.RDI, 0), x86_64.RAX)
                return
        }

        // Map user-facing built-in names to runtime symbol names (y_* prefix).
        target := runtimeSymbolName(fnName)

        sysvArgs := []x86_64.Reg{
                x86_64.RDI, x86_64.RSI, x86_64.RDX,
                x86_64.RCX, x86_64.R8, x86_64.R9,
        }
        for i, arg := range args {
                if i < len(sysvArgs) {
                        loadVal(arg, sysvArgs[i])
                }
        }

        // NOTE: Values are now consistently tagged at the point of creation
        // (emitConst produces tagged int/bool values).  The old needsTagging
        // logic that re-tagged constants here has been removed to avoid
        // double-tagging.  Runtime functions receive properly tagged values
        // directly.

        // Emit E8 rel32 manually and record a linker relocation.
        // We cannot use a.CALL(fnName) because the assembler's internal fixup
        // system only resolves label-based fixups, not cross-function targets.
        callOffset := uint64(a.Offset())
        a.EmitBytes([]byte{0xE8, 0x00, 0x00, 0x00, 0x00})
        if relocs != nil {
                *relocs = append(*relocs, pendingReloc{
                        offset: callOffset + 1, // displacement starts after the E8 opcode
                        target: target,
                })
        }
}

// needsTagging returns true if the runtime function expects NaN-boxed tagged
// values as arguments.  These functions are the I/O builtins that take a raw
// Yilt value and need to dispatch on the tag.
//
// IMPORTANT: functions that already return tagged values (y_str_new, y_fp_new,
// y_table_new, etc.) must NOT have their return values re-tagged by the caller.
// Only user-level built-in names like "print"/"println" trigger tagging here.
func needsTagging(runtimeSym string) bool {
        switch runtimeSym {
        case "y_print", "y_println", "y_eprint", "y_eprintln",
                "y_len", "y_type_of", "y_is_nil", "y_panic", "y_assert",
                "y_error", "y_tab_set":
                return true
        }
        return false
}

// emitCallRegs is like emitCall but for indirect calls where the callee
// register is already set up (e.g. R11).
// runtimeSymbolName maps user-facing built-in names to the y_* runtime symbol names
// used by the linker.  User-defined function names pass through unchanged.
func runtimeSymbolName(name string) string {
        switch name {
        case "print", "println", "eprint", "eprintln":
                return "y_" + name
        case "input":
                return "y_" + name
        case "len", "to_str", "to_int", "to_fp", "char", "ord", "format":
                return "y_" + name
        case "typeof", "is_nil":
                return "y_" + name
        case "abs", "sqrt", "floor", "ceil", "round", "trunc", "fract", "sign":
                return "y_" + name
        case "min", "max", "clamp", "pow", "log", "log2", "log10", "exp":
                return "y_" + name
        case "error":
                return "y_error"
        case "has", "keys", "values":
                return "y_" + name
        default:
                return name
        }
}

func emitCallRegs(a *x86_64.Asm, args []*ir.Value,
        loadVal func(*ir.Value, x86_64.Reg)) {

        sysvArgs := []x86_64.Reg{
                x86_64.RDI, x86_64.RSI, x86_64.RDX,
                x86_64.RCX, x86_64.R8, x86_64.R9,
        }
        for i, arg := range args {
                if i < len(sysvArgs) {
                        loadVal(arg, sysvArgs[i])
                }
        }
}

// emitBlockArgCopies emits MOV instructions to copy BranchTarget.Args
// into the corresponding Block.Params stack slots.
func emitBlockArgCopies(a *x86_64.Asm, bt *ir.BranchTarget,
        slots map[int]int,
        loadVal func(*ir.Value, x86_64.Reg),
        storeVal func(*ir.Value, x86_64.Reg)) {

        if bt.Block == nil {
                return
        }
        for i, arg := range bt.Args {
                if i < len(bt.Block.Params) {
                        loadVal(arg, x86_64.RAX)
                        storeVal(bt.Block.Params[i], x86_64.RAX)
                }
        }
}

// emitAArch64 generates AArch64 machine code by translating the IR function
// through the AArch64 codegen bridge.
func emitAArch64(fn *ir.Func) []byte {
        cg := aarch64.NewCodeGen()
        return cg.GenerateFunc(fn)
}

// emitRV64 generates RISC-V 64 machine code.
func emitRV64(fn *ir.Func) []byte {
        var buf []byte
        buf = appendLE32(buf, 0x1141)     // addi sp, sp, -16
        buf = appendLE32(buf, 0xE022)     // sd ra, 8(sp)
        buf = appendLE32(buf, 0xE126)     // sd s0, 0(sp)
        buf = appendLE32(buf, 0x0485)     // addi s0, sp, 16
        buf = appendLE32(buf, 0x4505)     // li a0, 0
        buf = appendLE32(buf, 0x6422)     // ld s0, 0(sp)
        buf = appendLE32(buf, 0x60E2)     // ld ra, 8(sp)
        buf = appendLE32(buf, 0x0141)     // addi sp, sp, 16
        buf = appendLE32(buf, 0x8082)     // ret
        return buf
}

// emitRV32 generates RISC-V 32 machine code.
func emitRV32(fn *ir.Func) []byte {
        var buf []byte
        buf = appendLE32(buf, 0x1141)     // addi sp, sp, -16
        buf = appendLE32(buf, 0xE022)     // sw ra, 12(sp)
        buf = appendLE32(buf, 0xE426)     // sw s0, 8(sp)
        buf = appendLE32(buf, 0x0485)     // addi s0, sp, 16
        buf = appendLE32(buf, 0x4505)     // li a0, 0
        buf = appendLE32(buf, 0x6422)     // lw s0, 8(sp)
        buf = appendLE32(buf, 0x60E2)     // lw ra, 12(sp)
        buf = appendLE32(buf, 0x0141)     // addi sp, sp, 16
        buf = appendLE32(buf, 0x8082)     // ret
        return buf
}

// emitWASM generates WebAssembly bytecode for a function.
func emitWASM(fn *ir.Func) []byte {
        var buf []byte
        // WASM function body: local.get 0 (for params), i32.const 0, end
        buf = append(buf, 0x00) // 0 local decls
        buf = append(buf, 0x41) // i32.const
        buf = append(buf, 0x00) // 0
        buf = append(buf, 0x0B) // end
        return buf
}

func appendLE32(buf []byte, v uint32) []byte {
        return append(buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// ========== Linker ==========

func createLinker(tgt target.Target) link.Linker {
        cfg := link.Config{
                Target: tgt.Triple,
                PIE:    false, // Yilt produces static binaries; ET_EXEC is required
        }
        lnk, err := link.NewLinker(cfg)
        if err != nil {
                fmt.Fprintf(os.Stderr, "error: cannot create linker: %s\n", err)
                return nil
        }
        return lnk
}

// addStartAndRuntime generates the _start entry stub and basic runtime support.
//
// The _start stub is what the kernel jumps to. It calls main(), then issues
// an exit syscall with the return value.
//
// Runtime stubs provide minimal implementations of the most common builtins
// (y_print, y_str_new, etc.) so that basic programs can actually run without
// an external runtime library.
func addStartAndRuntime(lnk link.Linker, tgt target.Target) {
        switch tgt.Arch {
        case "x86_64":
                addStartX86_64(lnk)
                addRuntimeX86_64(lnk)
        default:
                // For other architectures, add stub symbols at addr 0.
                // The binary will link but runtime calls will crash.
                addRuntimeSymbols(lnk)
        }
}

// addStartX86_64 emits a minimal _start stub for x86-64 Linux.
//
// Assembly:
//   _start:
//     xor  ebp, ebp          ; clear frame pointer
//     call main               ; call user's main
//     mov  edi, eax           ; exit code = return value
//     mov  eax, 60            ; SYS_exit
//     syscall
func addStartX86_64(lnk link.Linker) {
        a := x86_64.NewAsm()

        // xor ebp, ebp
        a.XorRR(x86_64.RBP, x86_64.RBP)

        // Align stack to 16 bytes before calling main.
        // The kernel gives us a 16-byte aligned stack, but the CALL instruction
        // in _start will push 8 bytes (return address, though we never return),
        // making RSP 8-mod-16 at main entry. Fix by AND-ing RSP.
        a.AndRI(x86_64.RSP, -16) // AND RSP, 0xFFFFFFFFFFFFFFF0

        // Record offset of the CALL instruction so we can add a relocation.
        // The assembler's CALL creates a fixup that won't be resolved within
        // this single assembly session (main is in a different section), so we
        // manually emit the E8 opcode and add a linker relocation instead.
        callOffset := uint64(a.Offset())

        // call main — E8 rel32 (placeholder 0, patched by linker)
        a.EmitBytes([]byte{0xE8, 0x00, 0x00, 0x00, 0x00})

        // mov edi, eax — exit code = main's return value
        a.MovRR(x86_64.RDI, x86_64.RAX)

        // mov eax, 60 — SYS_exit
        a.MovRM(x86_64.RAX, 60)

        // syscall
        a.SYSCALL()

        lnk.AddCode("_start", a.Bytes())
        // Add a PC32 relocation so the linker patches the CALL to point to main.
        // The relocation is at the start of the 4-byte displacement (callOffset+1),
        // targeting the "main" section, with addend -4 because PC32 is S+A-P.
        lnk.AddRelocation("_start", callOffset+1, "main", link.RelocPC32, -4)
}

// addRuntimeX86_64 adds the compiled C runtime functions to the linker.
//
// The runtime is compiled from internal/runtime/cruntime/runtime.c to a raw
// .text binary that is embedded in the Go binary.  Each function is added as
// a separate code section so the existing linker resolves inter-function
// calls (e.g. y_println → y_print) via standard PC32 relocations.
//
// String literals used by the runtime (.rodata.str1.1) are added as a single
// read-only data section.  The runtime's internal relocations (absolute and
// PC-relative references to string literals and between functions) are
// translated to linker relocations and applied during the link phase.
func addRuntimeX86_64(lnk link.Linker) {
        // 1. Add each runtime function as a separate code section.
        // Include local (static) helper functions that are called by the
        // global functions via pre-resolved PC-relative offsets. These must
        // be added in blob-offset order so the linker places them contiguously,
        // preserving the relative call offsets compiled into the .text blob.
        for _, name := range yiltruntime.GetMergedAllFunctions() {
                code := yiltruntime.GetMergedFunctionCode(name)
                if code != nil {
                        lnk.AddCode(name, code)
                }
        }

        // 2. Add the .rodata.str1.1 section (string literals).
        rodataStr := yiltruntime.GetMergedRodataStr()
        if len(rodataStr) > 0 {
                        lnk.AddData(".yilt.rt.rodata.str", rodataStr)
        }

        // 3. Add the .rodata section (jump tables).
        rodata := yiltruntime.GetRuntimeRodata()
        if len(rodata) > 0 {
                lnk.AddData(".yilt.rt.rodata", rodata)
        }

        // 3b. Add writable runtime data section for iterator globals.
        //     Layout: +0 g_iter_table, +8 g_iter_result_key,
        //             +16 g_iter_result_val, +24 g_iter_next_idx
        const rtDataSize = 32
        rtData := make([]byte, rtDataSize) // zero-initialised
        lnk.AddRWData(".yilt.rt.data", rtData)

        // 3c. Add constant data sections used by float operations.
        rodataCst8 := yiltruntime.GetRuntimeRodataCst8()
        if len(rodataCst8) > 0 {
                lnk.AddData(".yilt.rt.rodata.cst8", rodataCst8)
        }
        rodataCst16 := yiltruntime.GetRuntimeRodataCst16()
        if len(rodataCst16) > 0 {
                lnk.AddData(".yilt.rt.rodata.cst16", rodataCst16)
        }
        rodataCst4 := yiltruntime.GetRuntimeRodataCst4()
        if len(rodataCst4) > 0 {
                lnk.AddData(".yilt.rt.rodata.cst4", rodataCst4)
        }

        // 4. Translate and register the runtime's internal relocations.
        //    These patch absolute addresses of string literals and PC-relative
        //    calls between runtime functions within the compiled .text blob.
        relocations := yiltruntime.GetMergedRelocations()
        for _, r := range relocations {
                // Find which function section this relocation belongs to.
                funcName, adjustedOffset := resolveRuntimeFunc(r.Offset)
                if funcName == "" {
                        continue // orphan relocation, skip
                }

                switch {
                case r.Target == yiltruntime.TargetRodataStr:
                        // Absolute reference to string literal data.
                        lnk.AddRelocation(funcName, adjustedOffset,
                                ".yilt.rt.rodata.str",
                                types.RelocAbs32, int64(r.Addend))

                case r.Target == yiltruntime.TargetRodata:
                        // Absolute reference to rodata (jump tables).
                        lnk.AddRelocation(funcName, adjustedOffset,
                                ".yilt.rt.rodata",
                                types.RelocAbs32S, int64(r.Addend))

                case r.Target == yiltruntime.TargetText && r.Symbol != "":
                        // PC-relative call to another runtime function.
                        lnk.AddRelocation(funcName, adjustedOffset,
                                r.Symbol,
                                types.RelocPC32, -4)

                case r.Target == yiltruntime.TargetRodataCst8:
                        // PC-relative reference to 8-byte float constants.
                        lnk.AddRelocation(funcName, adjustedOffset,
                                ".yilt.rt.rodata.cst8",
                                types.RelocPC32, int64(r.Addend))

                case r.Target == yiltruntime.TargetRodataCst16:
                        // PC-relative reference to 16-byte float constants.
                        lnk.AddRelocation(funcName, adjustedOffset,
                                ".yilt.rt.rodata.cst16",
                                types.RelocPC32, int64(r.Addend))

                case r.Target == yiltruntime.TargetRodataCst4:
                        // PC-relative reference to 4-byte constants.
                        lnk.AddRelocation(funcName, adjustedOffset,
                                ".yilt.rt.rodata.cst4",
                                types.RelocPC32, int64(r.Addend))
                }
        }

        // 4b. Process pure-Go runtime relocations (per-function, offset-relative).
        //     Puregen functions are separate code sections added via AddCode, so
        //     their relocations reference offsets within each function's own code blob.
        //     Puregen rodata strings are appended after C rodata, so we add the
        //     base offset to rodata relocation addends.
        puregenRelocsByFunc := yiltruntime.PuregenRelocationsByFunc()
        pgRodataBase := yiltruntime.PuregenRodataBaseOffset()
        for funcName, relocs := range puregenRelocsByFunc {
                for _, pr := range relocs {
                        switch pr.Target {
                        case ".yilt.rt.rodata.str":
                                // PC-relative reference to rodata string data.
                                // Adjust addend: puregen offset is relative to start of
                                // puregen rodata, but the section includes C rodata first.
                                lnk.AddRelocation(funcName, uint64(pr.Offset),
                                        ".yilt.rt.rodata.str",
                                        types.RelocPC32, int64(pr.Addend+pgRodataBase)-4)
                        case ".yilt.rt.data":
                                // Absolute 32-bit reference to writable runtime data
                                // (e.g., iterator globals: g_iter_table, result_key/val, next_idx).
                                lnk.AddRelocation(funcName, uint64(pr.Offset),
                                        ".yilt.rt.data",
                                        types.RelocAbs32S, int64(pr.Addend))
                        default:
                                // PLT32: cross-function call (pr.Symbol = target func name).
                                if pr.Symbol != "" {
                                        lnk.AddRelocation(funcName, uint64(pr.Offset),
                                                pr.Symbol,
                                                types.RelocPC32, -4)
                                }
                        }
                }
        }

        // 5. Translate and register .rodata internal relocations.
        //    These are 64-bit absolute relocations that write function addresses
        //    into the jump table (e.g., y_type_of's switch table).
        //    Each entry has a TextOffset (byte position within the original .text
        //    blob) which we resolve to a function name + offset within that function.
        rodataRelocs := yiltruntime.GetRuntimeRodataRelocations()
        for _, rr := range rodataRelocs {
                funcName, offsetInFunc := resolveRuntimeFunc(uint64(rr.TextOffset))
                if funcName != "" {
                        lnk.AddRelocation(".yilt.rt.rodata", rr.Offset,
                                funcName,
                                types.RelocAbs64, int64(offsetInFunc))
                }
        }
}

// resolveRuntimeFunc maps a .text blob offset to the function name and the
// offset within that function's section.  Returns ("", 0) if the offset
// falls outside any known function.
func resolveRuntimeFunc(blobOffset uint64) (string, uint64) {
        syms := yiltruntime.GetAllSymbolNames()
        var bestName string
        var bestStart uint64
        for _, name := range syms {
                start := uint64(yiltruntime.GetFunctionOffset(name))
                end := start + yiltruntime.GetFunctionSize(name)
                if blobOffset >= start && blobOffset < end {
                        return name, blobOffset - start
                }
                        // Also handle relocations that land at the exact end of a function.
                if start <= blobOffset && blobOffset <= start+yiltruntime.GetFunctionSize(name) {
                        if bestName == "" || start > bestStart {
                                bestName = name
                                bestStart = start
                        }
                }
        }
        if bestName != "" {
                return bestName, blobOffset - bestStart
        }
        return "", 0
}
// addRuntimeSymbols registers phantom runtime symbols at address 0.
// This is used for targets where no _start stub is generated yet.
func addRuntimeSymbols(lnk link.Linker) {
        syms := []string{
                "y_arena_push", "y_arena_pop", "y_alloc", "y_free",
                "y_str_new", "y_str_concat", "y_to_str", "y_to_int", "y_to_fp",
                "y_fp_new", "y_copy", "y_promote",
                "y_val_eq", "y_type_of", "y_str_concat",
                "y_print", "y_println", "y_eprint", "y_eprintln", "y_input", "y_len", "y_panic", "y_assert",
                "y_error", "y_is_error",
                "y_table_new", "y_tab_set", "y_tab_get", "y_tab_has",
                "y_tab_del", "y_tab_len",
                "y_tab_iter_valid", "y_tab_iter_key", "y_tab_iter_val", "y_tab_iter_next",
                "y_abs", "y_neg", "y_min", "y_max",
                "y_sqrt", "y_floor", "y_ceil", "y_round", "y_sign", "y_clamp",
                "y_trim", "y_lower", "y_upper", "y_substr",
                "y_contains", "y_starts_with", "y_ends_with", "y_find",
                "y_sys_args", "y_sys_cwd", "y_sys_platform", "y_sys_env", "y_sys_exit",
                "y_sys_write", "y_sys_open", "y_sys_close",
                "y_fs_exists", "y_fs_read_text", "y_fs_write_text", "y_fs_append_text",
                "y_fs_read_lines", "y_fs_remove", "y_fs_rename", "y_fs_copy",
                "y_fs_mkdir", "y_fs_rmdir", "y_fs_read_dir",
                "y_path_normalize", "y_path_resolve", "y_path_relative", "y_path_join",
                "y_path_dirname", "y_path_parent", "y_path_basename",
                "y_path_stem", "y_path_extname", "y_path_is_abs",
                "y_json_encode", "y_json_decode",
                "yilt_run", "yilt_main_abi",
        }
        for _, s := range syms {
                lnk.AddSymbol(s, 0, 0, link.SymFunc)
        }
}

// ========== Optimizations (One-Pass) ==========

func applyOptimizations(module *ir.Module, level int) {
        for _, fn := range module.Funcs {
                optimizeFunc(fn, level)
        }
}

func optimizeFunc(fn *ir.Func, level int) {
        for _, block := range fn.Blocks {
                // Constant folding
                newInstrs := make([]*ir.Instr, 0, len(block.Instrs))
                for _, instr := range block.Instrs {
                        if folded := tryConstantFold(instr); folded != nil {
                                newInstrs = append(newInstrs, folded)
                        } else {
                                newInstrs = append(newInstrs, instr)
                        }
                }
                block.Instrs = newInstrs

                // Dead code elimination
                for i, instr := range block.Instrs {
                        if instr.IsTerminator() && i+1 < len(block.Instrs) {
                                block.Instrs = block.Instrs[:i+1]
                                break
                        }
                }
        }
}

func tryConstantFold(instr *ir.Instr) *ir.Instr {
        if instr.NoFold {
                return nil
        }
        if len(instr.Src) < 2 || instr.Dest == nil {
                return nil
        }
        s0, s1 := instr.Src[0], instr.Src[1]
        if s0.Const == nil || s1.Const == nil {
                return nil
        }
        if s0.Const.Kind != ir.VInt || s1.Const.Kind != ir.VInt {
                return nil
        }
        a, b := s0.Const.IntVal, s1.Const.IntVal

        var result int64
        switch instr.Op {
        case ir.OpAdd:
                result = a + b
        case ir.OpSub:
                result = a - b
        case ir.OpMul:
                result = a * b
        case ir.OpDiv:
                if b == 0 {
                        return nil
                }
                result = a / b
        case ir.OpMod:
                if b == 0 {
                        return nil
                }
                result = a % b
        case ir.OpAnd:
                result = a & b
        case ir.OpOr:
                result = a | b
        case ir.OpXor:
                result = a ^ b
        case ir.OpShl:
                result = a << uint64(b&63)
        case ir.OpShr:
                result = a >> uint64(b&63)
        default:
                return nil
        }

        // Modify the existing Dest value in-place to be a constant, rather than
        // creating a new Value object.  This ensures that any other IR
        // instructions that reference this value (e.g. Call args) will see
        // the constant when loadVal checks IsConst() during codegen.
        instr.Dest.Const = &ir.ConstVal{Kind: ir.VInt, IntVal: result}
        instr.Dest.Type = ir.VInt
        return &ir.Instr{Op: ir.OpConstInt, Dest: instr.Dest}
}

// ========== Helpers ==========

func runBinary(absPath string, inputName string) int {
        cmd := exec.Command(absPath)
        cmd.Stdout = os.Stdout
        cmd.Stderr = os.Stderr
        err := cmd.Run()
        if err != nil {
                if exitErr, ok := err.(*exec.ExitError); ok {
                        // Check if the process was killed by a signal
                        if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
                                fmt.Fprintf(os.Stderr, "Error: %s crashed (signal %d)\n", inputName, status.Signal())
                                return 128 + int(status.Signal())
                        }
                        code := exitErr.ExitCode()
                        if code != 0 {
                                fmt.Fprintf(os.Stderr, "Error: %s exited with code %d\n", inputName, code)
                                return code
                        }
                }
                fmt.Fprintf(os.Stderr, "error: failed to run %s: %s\n", inputName, err)
                return 1
        }
        return 0
}

// ========== CLI Output Helpers ==========

// plural returns "s" for values other than 1.
func plural(n int) string {
        if n != 1 {
                return "s"
        }
        return ""
}

// formatDuration formats a duration for display.
// Small durations use µs/ms, larger ones use seconds.
func formatDuration(d time.Duration) string {
        if d < time.Microsecond {
                return fmt.Sprintf("%.2fns", float64(d))
        }
        if d < time.Millisecond {
                return fmt.Sprintf("%.2fµs", float64(d)/float64(time.Microsecond))
        }
        if d < time.Second {
                return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
        }
        return fmt.Sprintf("%.3fs", d.Seconds())
}

// phaseLine prints a single compilation phase result line to stderr.
func phaseLine(isTTY bool, name string, dur time.Duration, detail string) {
        if isTTY {
                fmt.Fprintf(os.Stderr, "  %s%-12s%s  %s%s%s  %s\n",
                        cliColorBold, name, cliColorReset,
                        cliColorGreen, formatDuration(dur), cliColorReset,
                        detail)
        } else {
                fmt.Fprintf(os.Stderr, "  %-12s  %s  %s\n",
                        name, formatDuration(dur), detail)
        }
}

// printAbortFooter prints the error/warning count footer after diagnostics.
func printAbortFooter(isTTY bool, dh *diag.DiagnosticHandler) {
        ec := dh.ErrorCount()
        wc := dh.WarningCount()
        if isTTY {
                fmt.Fprintf(os.Stderr, "\n  %sAborted: %s %d error%s, %d warning%s%s\n",
                        cliColorBold+cliColorYellow,
                        cliColorReset,
                        ec, plural(ec), wc, plural(wc),
                        cliColorReset)
                fmt.Fprintf(os.Stderr, "  %sFor help, run: %syiltc --help%s\n",
                        cliColorDim, cliColorCyan, cliColorReset)
        } else {
                fmt.Fprintf(os.Stderr, "\n  Aborted: %d error%s, %d warning%s\n",
                        ec, plural(ec), wc, plural(wc))
                fmt.Fprintf(os.Stderr, "  For help, run: yiltc --help\n")
        }
}

// printSuccessLine prints the compilation success summary to stderr.
func printSuccessLine(isTTY bool, input, output string, tgt target.Target, elapsed time.Duration, binarySize int64) {
        base := filepath.Base(input)
        dur := formatDuration(elapsed)
        sizeStr := formatSize(binarySize)
        targetInfo := fmt.Sprintf("%s-%s, %s, %s", tgt.Arch, tgt.OS, tgt.Format, sizeStr)
        if isTTY {
                line := fmt.Sprintf("  %s %s → %s (%s) in %s\n",
                        cliColorGreen+"Compiled"+cliColorReset,
                        cliColorBold+base+cliColorReset,
                        cliColorBold+output+cliColorReset,
                        cliColorDim+targetInfo+cliColorReset,
                        cliColorGreen+dur+cliColorReset)
                fmt.Fprint(os.Stderr, line)
        } else {
                fmt.Fprintf(os.Stderr, "  Compiled %s → %s (%s) in %s\n",
                        base, output, targetInfo, dur)
        }
}

// ========== Usage / Help ==========

// flagInfo describes a single command-line flag for custom help formatting.
type flagInfo struct {
        names    []string // all names (e.g. ["-o", "--output"])
        usage    string
        defValue string // empty means no default shown
}

// printUsage displays the friendly usage message with custom formatting.
func printUsage(fs *flag.FlagSet) {
        isTTY := isTerminal(os.Stderr)
        b := cliColorBold
        g := cliColorGreen
        c := cliColorCyan
        d := cliColorDim
        r := cliColorReset

        // Header
        if isTTY {
                fmt.Fprintf(os.Stderr, "%sYilt Compiler v%s%s — A fast, zero-dependency systems language\n\n", b, version, r)
        } else {
                fmt.Fprintf(os.Stderr, "Yilt Compiler v%s — A fast, zero-dependency systems language\n\n", version)
        }

        // Usage examples
        fmt.Fprintf(os.Stderr, "Usage:\n")
        fmt.Fprintf(os.Stderr, "  yiltc [options] <input.yilt>        Compile a Yilt source file\n")
        if isTTY {
                fmt.Fprintf(os.Stderr, "  %syiltc --run <input.yilt>%s           Compile and run immediately\n", c, r)
                fmt.Fprintf(os.Stderr, "  %syiltc --check <input.yilt>%s         Type-check without compiling\n", c, r)
                fmt.Fprintf(os.Stderr, "  %syiltc --eval 'expr'%s                Evaluate a single expression\n", c, r)
        } else {
                fmt.Fprintf(os.Stderr, "  yiltc --run <input.yilt>           Compile and run immediately\n")
                fmt.Fprintf(os.Stderr, "  yiltc --check <input.yilt>         Type-check without compiling\n")
                fmt.Fprintf(os.Stderr, "  yiltc --eval 'expr'                Evaluate a single expression\n")
        }

        // Quick start
        fmt.Fprintf(os.Stderr, "\nQuick start:\n")
        if isTTY {
                fmt.Fprintf(os.Stderr, "  %syiltc hello.yilt%s              Compile to ./hello\n", g, r)
                fmt.Fprintf(os.Stderr, "  %syiltc hello.yilt --run%s        Compile and run immediately\n", g, r)
                fmt.Fprintf(os.Stderr, "  %syiltc hello.yilt -o out%s       Compile to ./out\n", g, r)
                fmt.Fprintf(os.Stderr, "  %syiltc hello.yilt -v%s           Show compilation phases\n", g, r)
                fmt.Fprintf(os.Stderr, "  %syiltc hello.yilt --check%s      Type-check without compiling\n", g, r)
        } else {
                fmt.Fprintf(os.Stderr, "  yiltc hello.yilt              Compile to ./hello\n")
                fmt.Fprintf(os.Stderr, "  yiltc hello.yilt --run        Compile and run immediately\n")
                fmt.Fprintf(os.Stderr, "  yiltc hello.yilt -o out       Compile to ./out\n")
                fmt.Fprintf(os.Stderr, "  yiltc hello.yilt -v           Show compilation phases\n")
                fmt.Fprintf(os.Stderr, "  yiltc hello.yilt --check      Type-check without compiling\n")
        }

        // Custom flag table — grouped for readability
        flags := []struct {
                group string
                items []flagInfo
        }{
                {
                        group: "Compilation",
                        items: []flagInfo{
                                {names: []string{"-o", "--output"}, usage: "Output file path", defValue: "<auto>"},
                                {names: []string{"-t", "--target"}, usage: "Target triple", defValue: "<host>"},
                                {names: []string{"-O", "--optimize"}, usage: "Optimization level (0=none, 1=basic, 2=more)", defValue: "1"},
                                {names: []string{"-j", "--jobs"}, usage: "Concurrent compilation jobs", defValue: fmt.Sprintf("%d", runtime.NumCPU())},
                        },
                },
                {
                        group: "Output control",
                        items: []flagInfo{
                                {names: []string{"--run"}, usage: "Compile and immediately execute"},
                                {names: []string{"--check"}, usage: "Type-check only (no codegen or linking)"},
                                {names: []string{"-e", "--eval"}, usage: "Evaluate a single expression"},
                                {names: []string{"--emit-ir"}, usage: "Emit IR to stdout"},
                                {names: []string{"--emit-ast"}, usage: "Emit AST to stderr"},
                                {names: []string{"--emit-obj"}, usage: "Emit object file only (no linking)"},
                        },
                },
                {
                        group: "Diagnostics",
                        items: []flagInfo{
                                {names: []string{"-v", "--verbose"}, usage: "Show compilation phase details and timing"},
                                {names: []string{"--quiet"}, usage: "Suppress success and progress messages"},
                                {names: []string{"-W"}, usage: "Warning control: all, none, or comma-separated codes", defValue: "all"},
                                {names: []string{"--Werror"}, usage: "Treat warnings as errors"},
                        },
                },
                {
                        group: "Information",
                        items: []flagInfo{
                                {names: []string{"--version"}, usage: "Print compiler version and exit"},
                                {names: []string{"--list-targets"}, usage: "List all supported target triples"},
                                {names: []string{"-h", "--help"}, usage: "Show this help message"},
                        },
                },
        }

        fmt.Fprintf(os.Stderr, "\nOptions:\n")
        for _, group := range flags {
                fmt.Fprintf(os.Stderr, "  %s%s:%s\n", d, group.group, r)
                for _, item := range group.items {
                        // Format: "    -o, --output   description     (default: ...)"
                        nameStr := strings.Join(item.names, ", ")
                        pad := 22 - len(nameStr)
                        if pad < 2 {
                                pad = 2
                        }
                        line := fmt.Sprintf("    %s%s%s", nameStr, strings.Repeat(" ", pad), item.usage)
                        if item.defValue != "" {
                                line += fmt.Sprintf("  (default: %s)", item.defValue)
                        }
                        fmt.Fprintf(os.Stderr, "  %s\n", line)
                }
        }

        // Targets
        fmt.Fprintf(os.Stderr, "\nTargets:\n")
        for _, t := range target.AllTargets() {
                if isTTY {
                        fmt.Fprintf(os.Stderr, "  %s%-30s%s %s\n", d, t.Triple, r, t.String())
                } else {
                        fmt.Fprintf(os.Stderr, "  %-30s %s\n", t.Triple, t.String())
                }
        }
}

// ========== File Suggestions ==========

// suggestFiles prints similar .yilt files from the input file's directory
// when the user provides a non-existent filename.
func suggestFiles(input string) {
        // Search in the directory of the requested file, not just cwd.
        searchDir := "."
        baseInput := filepath.Base(input)
        dirInput := filepath.Dir(input)
        if dirInput != "." && dirInput != "" {
                if info, err := os.Stat(dirInput); err == nil && info.IsDir() {
                        searchDir = dirInput
                }
        }

        entries, err := os.ReadDir(searchDir)
        if err != nil {
                return
        }
        inputLower := strings.ToLower(baseInput)
        var candidates []string
        for _, e := range entries {
                name := e.Name()
                if !e.IsDir() && strings.HasSuffix(name, ".yilt") {
                        base := strings.TrimSuffix(name, ".yilt")
                        baseLower := strings.ToLower(base)
                        if strings.HasPrefix(baseLower, inputLower) ||
                                strings.HasPrefix(inputLower, baseLower) ||
                                strings.Contains(baseLower, inputLower) ||
                                containsSimilar(base, baseInput) > 0 {
                                if searchDir != "." {
                                        candidates = append(candidates, filepath.Join(searchDir, name))
                                } else {
                                        candidates = append(candidates, name)
                                }
                        }
                }
        }
        if len(candidates) > 0 {
                isTTY := isTerminal(os.Stderr)
                if len(candidates) <= 5 {
                        if isTTY {
                                fmt.Fprintf(os.Stderr, "  %sDid you mean one of these?%s\n", cliColorCyan, cliColorReset)
                        } else {
                                fmt.Fprintf(os.Stderr, "  Did you mean one of these?\n")
                        }
                } else {
                        candidates = candidates[:5]
                        fmt.Fprintf(os.Stderr, "  Similar files:\n")
                }
                for _, c := range candidates {
                        fmt.Fprintf(os.Stderr, "    %s\n", c)
                }
        }
}

// containsSimilar checks if two filenames are similar by looking at
// common prefix length and character overlap.
func containsSimilar(a, b string) int {
        minLen := len(a)
        if len(b) < minLen {
                minLen = len(b)
        }
        if minLen < 3 {
                return 0
        }
        common := 0
        for i := 0; i < minLen; i++ {
                if strings.ToLower(string(a[i])) == strings.ToLower(string(b[i])) {
                        common++
                } else {
                        break
                }
        }
        if common >= minLen/2+1 {
                return common
        }
        return 0
}

// ========== Eval Mode ==========

// runEval implements the --eval flag by writing a temporary .yilt file,
// compiling and running it, then cleaning up.
func runEval(expr, output, targetStr string, optLevel int, verbose bool, jobs int, warnControl string, werror bool) int {
        // Build the temp source code
        src := fmt.Sprintf("fn main()\n    print(%s)\n", expr)

        // Create a temp file
        tmpFile, err := os.CreateTemp("", "yilt_eval_*.yilt")
        if err != nil {
                fmt.Fprintf(os.Stderr, "error: cannot create temp file: %s\n", err)
                return 1
        }
        tmpPath := tmpFile.Name()
        defer os.Remove(tmpPath)
        defer os.Remove(strings.TrimSuffix(tmpPath, ".yilt"))

        if _, err := tmpFile.WriteString(src); err != nil {
                tmpFile.Close()
                fmt.Fprintf(os.Stderr, "error: cannot write temp file: %s\n", err)
                return 1
        }
        tmpFile.Close()

        // Build args for compile+run (suppress normal output with quiet mode)
        evalArgs := []string{tmpPath, "--run", "--quiet"}
        if output != "" {
                evalArgs = append(evalArgs, "-o", output)
        }
        if targetStr != "" {
                evalArgs = append(evalArgs, "-t", targetStr)
        }
        if optLevel != 1 {
                evalArgs = append(evalArgs, fmt.Sprintf("-O%d", optLevel))
        }
        if verbose {
                evalArgs = append(evalArgs, "-v")
        }
        if jobs != runtime.NumCPU() {
                evalArgs = append(evalArgs, "-j", fmt.Sprintf("%d", jobs))
        }
        if warnControl != "all" {
                evalArgs = append(evalArgs, "-W", warnControl)
        }
        if werror {
                evalArgs = append(evalArgs, "-Werror")
        }

        return run(evalArgs)
}

// ========== Size Formatting ==========

// formatSize formats a byte count as a human-readable string (e.g. "14.2KB").
func formatSize(bytes int64) string {
        if bytes <= 0 {
                return "0B"
        }
        if bytes < 1024 {
                return fmt.Sprintf("%dB", bytes)
        }
        if bytes < 1024*1024 {
                return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
        }
        return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

// ========== Terminal Detection ==========

func isTerminal(w io.Writer) bool {
        f, ok := w.(interface{ Fd() uintptr })
        if !ok {
                return false
        }
        fi, err := os.Stat(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
        if err != nil {
                return false
        }
        return fi.Mode()&os.ModeDevice != 0
}
