package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

var (
	flagOut     = flag.String("o", "", "Output file, else stdout.")
	flagPkg     = flag.String("pkg", "main", "Package.")
	flagPrefix  = flag.String("prefix", "", "Prefix to strip from filesnames.")
	flagIgnore  = flag.String("ignore", "", "Regexp for files we should ignore (for example \\\\.DS_Store).")
	flagInclude = flag.String("include", "", "Regexp for files to include. Only files that match will be included.")
	flagModTime = flag.String("modtime", "", "Unix timestamp to override as modification time for all files.")
	flagPrivate = flag.Bool("private", false, "If true, do not export autogenerated functions.")
	modTime     *int64
)

type headerTemplateParams struct {
	PackageName    string
	FunctionPrefix string
}

type _escFile struct {
	data     []byte
	local    string
	fileinfo os.FileInfo
}

func main() {
	flag.Parse()
	var err error
	if *flagModTime != "" {
		i, err := strconv.ParseInt(*flagModTime, 10, 64)
		if err != nil {
			log.Fatalf("modtime must be an integer: %v", err)
		}
		modTime = &i
	}
	var fnames, dirnames []string
	content := make(map[string]_escFile)
	prefix := filepath.ToSlash(*flagPrefix)
	var ignoreRegexp *regexp.Regexp
	if *flagIgnore != "" {
		ignoreRegexp, err = regexp.Compile(*flagIgnore)
		if err != nil {
			log.Fatal(err)
		}
	}
	var includeRegexp *regexp.Regexp
	if *flagInclude != "" {
		includeRegexp, err = regexp.Compile(*flagInclude)
		if err != nil {
			log.Fatal(err)
		}
	}
	for _, base := range flag.Args() {
		files := []string{base}
		for len(files) > 0 {
			fname := files[0]
			files = files[1:]
			if ignoreRegexp != nil && ignoreRegexp.MatchString(fname) {
				continue
			}
			f, err := os.Open(fname)
			if err != nil {
				log.Fatal(err)
			}
			fi, err := f.Stat()
			if err != nil {
				log.Fatal(err)
			}
			if fi.IsDir() {
				fis, err := f.Readdir(0)
				if err != nil {
					log.Fatal(err)
				}
				for _, fi := range fis {
					files = append(files, filepath.Join(fname, fi.Name()))
				}
			} else if includeRegexp == nil || includeRegexp.MatchString(fname) {
				b, err := ioutil.ReadAll(f)
				if err != nil {
					log.Fatal(err)
				}
				fpath := filepath.ToSlash(fname)
				n := strings.TrimPrefix(fpath, prefix)
				n = path.Join("/", n)
				content[n] = _escFile{data: b, local: fpath, fileinfo: fi}
				fnames = append(fnames, n)
			}
			f.Close()
		}
	}
	sort.Strings(fnames)
	w := os.Stdout
	if *flagOut != "" {
		if w, err = os.Create(*flagOut); err != nil {
			log.Fatal(err)
		}
		defer w.Close()
	}
	headerText, err := header(*flagPkg, !(*flagPrivate))
	if nil != err {
		log.Fatalf("failed to expand autogenerated code: %s", err)
	}
	if _, err := w.Write(headerText); err != nil {
		log.Fatalf("failed to write output: %s", err)
	}
	dirs := map[string]bool{"/": true}
	for _, fname := range fnames {
		f := content[fname]
		for b := path.Dir(fname); b != "/"; b = path.Dir(b) {
			dirs[b] = true
		}
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		if _, err := gw.Write(f.data); err != nil {
			log.Fatal(err)
		}
		if err := gw.Close(); err != nil {
			log.Fatal(err)
		}
		t := f.fileinfo.ModTime().Unix()
		if modTime != nil {
			t = *modTime
		}
		fmt.Fprintf(w, `
	%q: {
		FileName:   %q,
		FileSize:   %v,
		ModifyTime: %v,
		Compressed: %s,
	},%s`, fname, f.local, len(f.data), t, segment(&buf), "\n")
	}
	for d := range dirs {
		dirnames = append(dirnames, d)
	}
	sort.Strings(dirnames)
	for _, dir := range dirnames {
		local := path.Join(prefix, dir)
		if len(local) == 0 {
			local = "."
		}
		if dir == "/" {
			continue
		}

		fmt.Fprintf(w, `
	%q: {
		IsFolder: true,
		FileName: %q,
	},%s`, dir, local, "\n")
	}
	fmt.Fprint(w, footer)
}

func segment(s *bytes.Buffer) string {
	var b bytes.Buffer
	b64 := base64.NewEncoder(base64.StdEncoding, &b)
	b64.Write(s.Bytes())
	b64.Close()
	res := "`\n"
	chunk := make([]byte, 80)
	for n, _ := b.Read(chunk); n > 0; n, _ = b.Read(chunk) {
		res += string(chunk[0:n]) + "\n"
	}
	return res + "`"
}

func header(packageName string, enableExports bool) ([]byte, error) {
	functionPrefix := ""
	if !enableExports {
		functionPrefix = "_esc"
	}
	headerParams := headerTemplateParams{
		PackageName:    packageName,
		FunctionPrefix: functionPrefix,
	}
	tmpl, err := template.New("").Parse(headerTemplate)
	if nil != err {
		return nil, err
	}
	var b bytes.Buffer
	err = tmpl.Execute(&b, headerParams)
	if nil != err {
		return nil, err
	}
	return b.Bytes(), nil
}

const (
	headerTemplate = `package {{.PackageName}}

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	log "github.com/cihub/seelog"
	"github.com/infinitbyte/framework/core/util"
	"github.com/infinitbyte/framework/core/vfs"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"sync"
)

func (vfs StaticFS) prepare(name string) (*vfs.VFile, error) {
	name = path.Clean(name)
	f, present := data[name]
	if !present {
		return nil, os.ErrNotExist
	}
	var err error
	vfs.once.Do(func() {
		f.FileName = path.Base(name)

		if f.FileSize == 0 {
			return
		}
		var gr *gzip.Reader
		b64 := base64.NewDecoder(base64.StdEncoding, bytes.NewBufferString(f.Compressed))
		gr, err = gzip.NewReader(b64)
		if err != nil {
			log.Error(err)
			return
		}
		f.Data, err = ioutil.ReadAll(gr)

	})
	if err != nil {
		log.Error(err)
		return nil, err
	}
	return f, nil
}

func (vfs StaticFS) Open(name string) (http.File, error) {

	name = path.Clean(name)

	if vfs.CheckLocalFirst {

		name = util.TrimLeftStr(name, vfs.TrimLeftPath)

		localFile := path.Join(vfs.StaticFolder, name)

		log.Trace("check local file, ", localFile)

		if util.FileExists(localFile) {

			f2, err := os.Open(localFile)
			if err == nil {
				return f2, err
			}
		}

		log.Debug("local file not found,", localFile)
	}

	f, err := vfs.prepare(name)
	if err != nil {
		return nil, err
	}
	return f.File()
}

type StaticFS struct {
	once            sync.Once
	StaticFolder    string
	TrimLeftPath    string
	CheckLocalFirst bool
}

var data = map[string]*vfs.VFile{
`
	footer = `}
`
)