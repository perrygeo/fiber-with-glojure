package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gloathub/glojure/pkg/glj"
	"github.com/gloathub/glojure/pkg/lang"
	"github.com/gloathub/glojure/pkg/runtime"
	"github.com/gofiber/fiber/v2"
	fiberLogger "github.com/gofiber/fiber/v2/middleware/logger"
	fiberRecover "github.com/gofiber/fiber/v2/middleware/recover"
)

var (
	loadFileMu   sync.Mutex
	loadFileFn   lang.IFn
	loadFileOnce sync.Once
)

func pushBindings() {
	kvs := make([]any, 0, 8)
	for _, v := range []*lang.Var{
		lang.VarCurrentNS,
		lang.VarWarnOnReflection,
		lang.VarUncheckedMath,
		lang.VarDataReaders,
	} {
		kvs = append(kvs, v, v.Deref())
	}
	lang.PushThreadBindings(lang.NewMap(kvs...))
}

func setupGlojure(srcDir string) error {
	runtime.AddLoadPath(os.DirFS(srcDir))
	pushBindings()
	defer lang.PopThreadBindings()
	return nil
}

func loadFile(path string) error {
	loadFileMu.Lock()
	defer loadFileMu.Unlock()

	var initErr error
	loadFileOnce.Do(func() {
		core := lang.FindNamespace(lang.NewSymbol("clojure.core"))
		if core == nil {
			initErr = fmt.Errorf("clojure.core not found")
			return
		}
		v := core.FindInternedVar(lang.NewSymbol("load-file"))
		if v == nil {
			initErr = fmt.Errorf("load-file not found")
			return
		}
		loadFileFn = v.Get().(lang.IFn)
	})
	if initErr != nil {
		return initErr
	}

	pushBindings()
	defer lang.PopThreadBindings()

	var panicErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicErr = fmt.Errorf("panic loading %s: %v", path, r)
			}
		}()
		loadFileFn.Invoke(path)
	}()
	return panicErr
}

func loadFiles(srcDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".clj" || ext == ".cljc" || ext == ".glj" {
			log.Printf("loading %s", path)
			return loadFile(path)
		}
		return nil
	})
}

func parseJSON(body []byte) lang.IPersistentMap {
	if len(body) == 0 {
		return lang.NewMap()
	}
	var data map[string]any
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&data); err != nil {
		return lang.NewMap()
	}
	kvs := make([]any, 0, len(data)*2)
	for k, v := range data {
		kvs = append(kvs, lang.NewKeyword(k), v)
	}
	return lang.NewMap(kvs...)
}

func glojureHandler(namespace, fn string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		method := strings.ToLower(c.Route().Method)
		uri := string(c.Request().RequestURI())
		body := c.Request().Body()

		headerKvs := make([]any, 0, 32)
		c.Request().Header.VisitAll(func(key, value []byte) {
			headerKvs = append(headerKvs,
				lang.NewKeyword(strings.ToLower(string(key))),
				string(value))
		})
		headers := lang.NewMap(headerKvs...)

		queryString := string(c.Request().URI().QueryString())
		paramKvs := make([]any, 0, 16)
		c.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
			paramKvs = append(paramKvs, lang.NewKeyword(string(key)), string(value))
		})
		params := lang.NewMap(paramKvs...)

		var jsonParams lang.IPersistentMap
		ct := strings.ToLower(strings.SplitN(string(c.Request().Header.ContentType()), ";", 2)[0])
		if ct == "application/json" && (method == "post" || method == "put" || method == "patch") {
			jsonParams = parseJSON(body)
		}
		if jsonParams == nil {
			jsonParams = lang.NewMap()
		}

		req := lang.NewPersistentHashMap(
			lang.NewKeyword("request-method"), lang.NewKeyword(method),
			lang.NewKeyword("uri"), uri,
			lang.NewKeyword("path"), c.Path(),
			lang.NewKeyword("headers"), headers,
			lang.NewKeyword("query-string"), queryString,
			lang.NewKeyword("params"), params,
			lang.NewKeyword("json-params"), jsonParams,
			lang.NewKeyword("body"), body,
		)

		callback := glj.Var(namespace, fn)
		var result any
		var panicMsg string
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicMsg = fmt.Sprintf("%v", r)
				}
			}()
			result = callback.Invoke(req)
		}()

		if panicMsg != "" {
			log.Printf("glojure error: %s", panicMsg)
			return c.Status(500).SendString(panicMsg)
		}

		resultMap, ok := result.(*lang.Map)
		if !ok {
			return c.Status(500).SendString(fmt.Sprintf("handler must return a map, got %T", result))
		}

		// Status
		statusCode := 200
		if s := resultMap.ValAt(lang.NewKeyword("status")); s != nil {
			switch v := s.(type) {
			case int:
				statusCode = v
			case int64:
				statusCode = int(v)
			}
		}
		c.Status(statusCode)

		// Content-Type
		if ct := resultMap.ValAt(lang.NewKeyword("content-type")); ct != nil {
			if s, ok := ct.(string); ok {
				c.Set("Content-Type", s)
			}
		}

		// Additional headers
		if h := resultMap.ValAt(lang.NewKeyword("headers")); h != nil {
			if hm, ok := h.(*lang.Map); ok {
				for seq := hm.Seq(); seq != nil; seq = seq.Next() {
					entry := seq.First().(*lang.MapEntry)
					var name string
					switch k := entry.Key().(type) {
					case *lang.Keyword:
						name = k.Name()
					case string:
						name = k
					default:
						name = fmt.Sprint(k)
					}
					c.Set(name, fmt.Sprint(entry.Val()))
				}
			}
		}

		// Body
		b := resultMap.ValAt(lang.NewKeyword("body"))
		if b == nil {
			return c.Send(nil)
		}
		switch v := b.(type) {
		case []byte:
			return c.Send(v)
		case string:
			return c.SendString(v)
		default:
			return c.SendString(fmt.Sprint(v))
		}
	}
}

func main() {
	const srcDir = "src"
	const port = ":3000"
	const handlerNS = "app.core"
	const handlerFn = "handler"

	if err := setupGlojure(srcDir); err != nil {
		log.Fatalf("glojure setup failed: %v", err)
	}
	if err := loadFiles(srcDir); err != nil {
		log.Fatalf("failed to load source files: %v", err)
	}

	app := fiber.New()
	app.Use(fiberLogger.New())
	app.Use(fiberRecover.New(fiberRecover.Config{EnableStackTrace: true}))
	app.All("/*", glojureHandler(handlerNS, handlerFn))

	log.Printf("listening on http://127.0.0.1%s", port)
	log.Fatal(app.Listen(port))
}
