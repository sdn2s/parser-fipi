package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
)

const userAgent = "parser-fipi/1.0 (educational; Go)"

// Client ходит в открытый банк ФИПИ (ege.fipi.ru / oge.fipi.ru).
// Держит cookie-сессию (DDoS-Guard + фильтры банка хранятся в сессии)
// и выдерживает паузу между запросами, чтобы не нагружать сервер.
type Client struct {
	Host    string // ege.fipi.ru | oge.fipi.ru
	Delay   time.Duration
	Verbose bool
	SaveDir string // если задано, страницы выдачи сохраняются сюда (UTF-8)

	http    *http.Client
	lastReq time.Time
}

func NewClient(host string, delay time.Duration) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		Host:  host,
		Delay: delay,
		http:  &http.Client{Jar: jar, Timeout: 90 * time.Second},
	}
}

func (c *Client) BankURL() string { return "https://" + c.Host + "/bank/" }

func (c *Client) throttle() {
	if wait := c.Delay - time.Since(c.lastReq); wait > 0 {
		time.Sleep(wait)
	}
	c.lastReq = time.Now()
}

// fetch выполняет GET (form == nil) или POST и возвращает тело.
// Повторяет запрос при сетевых ошибках, 429/5xx и пустом ответе:
// первый запрос новой сессии иногда приходит пустым.
func (c *Client) fetch(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= 4; attempt++ {
		c.throttle()
		var req *http.Request
		var err error
		if form == nil {
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		} else {
			req, err = http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
			if req != nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
		}
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Referer", c.BankURL())

		if c.Verbose {
			fmt.Fprintf(os.Stderr, "→ %s %s %s\n", req.Method, rawURL, form.Encode())
		}
		resp, err := c.http.Do(req)
		if err == nil {
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch {
			case rerr != nil:
				err = rerr
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				err = fmt.Errorf("HTTP %d", resp.StatusCode)
			case resp.StatusCode != http.StatusOK:
				return nil, fmt.Errorf("%s: HTTP %d", rawURL, resp.StatusCode)
			case len(bytes.TrimSpace(body)) == 0:
				err = errors.New("пустой ответ")
			default:
				return body, nil
			}
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		backoff := time.Duration(attempt*attempt) * time.Second
		if c.Verbose {
			fmt.Fprintf(os.Stderr, "  попытка %d не удалась (%v), повтор через %v\n", attempt, err, backoff)
		}
		time.Sleep(backoff)
	}
	return nil, fmt.Errorf("%s: %w", rawURL, lastErr)
}

// fetchHTML загружает страницу банка и перекодирует её из windows-1251.
func (c *Client) fetchHTML(ctx context.Context, rawURL string, form url.Values) (string, error) {
	body, err := c.fetch(ctx, rawURL, form)
	if err != nil {
		return "", err
	}
	utf, err := charmap.Windows1251.NewDecoder().Bytes(body)
	if err != nil {
		return "", fmt.Errorf("перекодировка cp1251: %w", err)
	}
	return string(utf), nil
}

// Subjects возвращает список предметов банка (заодно прогревает сессию).
func (c *Client) Subjects(ctx context.Context) ([]Subject, error) {
	page, err := c.fetchHTML(ctx, c.BankURL(), nil)
	if err != nil {
		return nil, err
	}
	return parseSubjects(page), nil
}

// Themes возвращает все коды КЭС, доступные в фильтре предмета.
func (c *Client) Themes(ctx context.Context, proj string) ([]Theme, error) {
	page, err := c.fetchHTML(ctx, c.BankURL()+"index.php?proj="+url.QueryEscape(proj), nil)
	if err != nil {
		return nil, err
	}
	return parseThemes(page), nil
}

// Filter — параметры поиска, которые понимает questions.php.
type Filter struct {
	Themes []string // коды КЭС: "7.5", "2.1"
	Kinds  []string // ILI_STD_SHORT, ILI_STD_FULL, ...
	QID    string   // номер задания ФИПИ, например 40B442
}

// Search возвращает одну страницу выдачи (page с нуля) и общее число заданий.
// Фильтр отправляется в каждом запросе, поэтому порядок запросов не важен.
func (c *Client) Search(ctx context.Context, proj string, f Filter, page, pageSize int) (*QuestionsPage, error) {
	form := url.Values{}
	form.Set("search", "1")
	form.Set("proj", proj)
	form.Set("pagesize", strconv.Itoa(pageSize))
	form.Set("page", strconv.Itoa(page))
	form.Set("theme", strings.Join(f.Themes, ","))
	form.Set("qkind", strings.Join(f.Kinds, ","))
	form.Set("qid", f.QID)
	for _, k := range []string{"qlevel", "qsstruct", "qpos", "zid", "solved", "favorite", "blind"} {
		form.Set(k, "")
	}
	pageURL := c.BankURL() + "questions.php"
	doc, err := c.fetchHTML(ctx, pageURL, form)
	if err != nil {
		return nil, err
	}
	if c.SaveDir != "" {
		exam, _, _ := strings.Cut(c.Host, ".")
		name := filepath.Join(c.SaveDir, fmt.Sprintf("%s_%d.html", exam, page))
		if err := os.MkdirAll(c.SaveDir, 0o755); err == nil {
			_ = os.WriteFile(name, []byte(doc), 0o644)
		}
	}
	return parseQuestionsPage(doc, pageURL)
}

// Download сохраняет файл (картинку) в dst, если его там ещё нет.
func (c *Client) Download(ctx context.Context, rawURL, dst string) error {
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return nil
	}
	body, err := c.fetch(ctx, rawURL, nil)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
