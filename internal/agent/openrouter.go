package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type HistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type PromptRequest struct {
	Prompt        string           `json:"prompt"`
	Mode          string           `json:"mode"` // "agent", "investigate", "plan"
	Model         string           `json:"model"`
	SystemPrompt  string           `json:"systemPrompt"`
	AttachedFiles []string         `json:"attachedFiles"`
	History       []HistoryMessage `json:"history"`
}

type StreamEvent struct {
	Type         string   `json:"type"` // "log", "delta", "done", "error"
	Content      string   `json:"content,omitempty"`
	Command      string   `json:"command,omitempty"`
	Detail       string   `json:"detail,omitempty"`
	Success      bool     `json:"success,omitempty"`
	Message      string   `json:"message,omitempty"`
	ChangedFiles []string `json:"changedFiles,omitempty"`
}

type Runner struct {
	RepoDir    string
	APIKey     string
	GatewayURL string
	HTTPClient *http.Client
}

func NewRunner(repoDir, apiKey, gatewayURL string) *Runner {
	return &Runner{
		RepoDir:    repoDir,
		APIKey:     apiKey,
		GatewayURL: strings.TrimRight(gatewayURL, "/"),
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
	}
}

// ResolveSafePath guarantees target stays strictly inside repoDir (prevents path traversal).
func ResolveSafePath(repoDir, relPath string) (string, error) {
	baseAbs, err := filepath.Abs(repoDir)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relPath)))
	if filepath.IsAbs(cleaned) {
		return "", errors.New("absolute path tidak diizinkan")
	}
	targetAbs, err := filepath.Abs(filepath.Join(baseAbs, cleaned))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("path keluar dari folder repository")
	}
	return targetAbs, nil
}

// Execute runs an agentic tool-calling loop against OpenRouter, emitting StreamEvents.
func (r *Runner) Execute(ctx context.Context, req PromptRequest, emit func(StreamEvent)) error {
	if strings.TrimSpace(r.APIKey) == "" {
		return errors.New("OPENROUTER_API_KEY belum diisi di .env bridge PC")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "google/gemini-4-neon"
	}
	allowWrite := strings.ToLower(strings.TrimSpace(req.Mode)) == "agent"

	sysPrompt := strings.TrimSpace(req.SystemPrompt)
	if sysPrompt == "" {
		sysPrompt = "Kamu adalah Wynn, coding agent remote yang membantu menginspeksi dan mengedit repository lokal."
	}
	if allowWrite {
		sysPrompt += "\nMode: agent. Kamu BOLEH menggunakan tool list_files, read_file, dan write_file untuk mengedit file secara langsung."
	} else {
		sysPrompt += "\nMode: read-only (" + req.Mode + "). Gunakan hanya list_files dan read_file; JANGAN mengubah file."
	}
	if len(req.AttachedFiles) > 0 {
		sysPrompt += "\nFile fokus pengguna: " + strings.Join(req.AttachedFiles, ", ")
	}

	messages := []map[string]any{
		{"role": "system", "content": sysPrompt},
	}
	startIdx := 0
	if len(req.History) > 10 {
		startIdx = len(req.History) - 10
	}
	for _, h := range req.History[startIdx:] {
		if strings.TrimSpace(h.Content) == "" {
			continue
		}
		messages = append(messages, map[string]any{
			"role":    h.Role,
			"content": h.Content,
		})
	}
	messages = append(messages, map[string]any{
		"role":    "user",
		"content": req.Prompt,
	})

	tools := buildToolSchemas(allowWrite)
	changedSet := map[string]bool{}
	var finalAnswer strings.Builder

	emit(StreamEvent{
		Type:    "log",
		Command: fmt.Sprintf("agent [%s] -> %s", req.Mode, model),
		Detail:  "Terhubung ke OpenRouter dari PC host",
		Success: true,
	})

	for step := 0; step < 6; step++ {
		choice, err := r.callChatCompletion(ctx, model, messages, tools)
		if err != nil {
			// Some free models reject tool schemas; retry once without tools.
			if step == 0 && len(tools) > 0 && strings.Contains(strings.ToLower(err.Error()), "tool") {
				tools = nil
				choice, err = r.callChatCompletion(ctx, model, messages, nil)
			}
			if err != nil {
				return err
			}
		}

		if len(choice.ToolCalls) == 0 {
			finalAnswer.WriteString(choice.Content)
			emit(StreamEvent{
				Type:    "delta",
				Content: choice.Content,
			})
			break
		}

		messages = append(messages, map[string]any{
			"role":       "assistant",
			"content":    choice.Content,
			"tool_calls": choice.ToolCalls,
		})

		for _, tc := range choice.ToolCalls {
			result, detail, ok, wrotePath := r.runTool(tc.Function.Name, tc.Function.Arguments, allowWrite)
			if wrotePath != "" {
				changedSet[wrotePath] = true
			}
			emit(StreamEvent{
				Type:    "log",
				Command: fmt.Sprintf("%s(%s)", tc.Function.Name, truncate(tc.Function.Arguments, 48)),
				Detail:  detail,
				Success: ok,
			})
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": tc.ID,
				"content":      result,
			})
		}
	}

	changed := make([]string, 0, len(changedSet))
	for p := range changedSet {
		changed = append(changed, p)
	}
	emit(StreamEvent{
		Type:         "done",
		Content:      strings.TrimSpace(finalAnswer.String()),
		ChangedFiles: changed,
	})
	return nil
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type assistantChoice struct {
	Content   string
	ToolCalls []toolCall
}

func (r *Runner) callChatCompletion(
	ctx context.Context,
	model string,
	messages []map[string]any,
	tools []map[string]any,
) (assistantChoice, error) {
	bodyMap := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if len(tools) > 0 {
		bodyMap["tools"] = tools
		bodyMap["tool_choice"] = "auto"
	}
	payload, _ := json.Marshal(bodyMap)

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		r.GatewayURL+"/chat/completions",
		bytes.NewReader(payload),
	)
	if err != nil {
		return assistantChoice{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+r.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/wynnsea/smart-home")
	httpReq.Header.Set("X-Title", "WynnDiff Bridge")

	resp, err := r.HTTPClient.Do(httpReq)
	if err != nil {
		return assistantChoice{}, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return assistantChoice{}, fmt.Errorf("OpenRouter HTTP %d: %s", resp.StatusCode, truncate(string(raw), 240))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []toolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return assistantChoice{}, fmt.Errorf("gagal decode respons AI: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return assistantChoice{}, errors.New("AI mengembalikan choices kosong")
	}
	msg := parsed.Choices[0].Message
	return assistantChoice{
		Content:   msg.Content,
		ToolCalls: msg.ToolCalls,
	}, nil
}

func (r *Runner) runTool(name, argsJSON string, allowWrite bool) (result, detail string, ok bool, wrotePath string) {
	var args map[string]any
	_ = json.Unmarshal([]byte(argsJSON), &args)

	switch name {
	case "list_files":
		dir, _ := args["dir"].(string)
		if dir == "" {
			dir = "."
		}
		target, err := ResolveSafePath(r.RepoDir, dir)
		if err != nil {
			return "error: " + err.Error(), err.Error(), false, ""
		}
		var files []string
		_ = filepath.WalkDir(target, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				base := d.Name()
				if base == ".git" || base == "node_modules" || base == "build" || base == ".dart_tool" {
					return filepath.SkipDir
				}
				return nil
			}
			if len(files) >= 120 {
				return filepath.SkipAll
			}
			if rel, err := filepath.Rel(r.RepoDir, p); err == nil {
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		return strings.Join(files, "\n"), fmt.Sprintf("%d file ditemukan", len(files)), true, ""

	case "read_file":
		pathStr, _ := args["path"].(string)
		target, err := ResolveSafePath(r.RepoDir, pathStr)
		if err != nil {
			return "error: " + err.Error(), err.Error(), false, ""
		}
		data, err := os.ReadFile(target)
		if err != nil {
			return "error: " + err.Error(), err.Error(), false, ""
		}
		if len(data) > 64*1024 {
			data = data[:64*1024]
		}
		return string(data), fmt.Sprintf("%d bytes dibaca", len(data)), true, ""

	case "write_file":
		if !allowWrite {
			return "error: write_file dinonaktifkan di mode read-only", "ditolak (read-only)", false, ""
		}
		pathStr, _ := args["path"].(string)
		content, _ := args["content"].(string)
		target, err := ResolveSafePath(r.RepoDir, pathStr)
		if err != nil {
			return "error: " + err.Error(), err.Error(), false, ""
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "error: " + err.Error(), err.Error(), false, ""
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return "error: " + err.Error(), err.Error(), false, ""
		}
		rel, _ := filepath.Rel(r.RepoDir, target)
		slashRel := filepath.ToSlash(rel)
		return "ok: file ditulis " + slashRel, fmt.Sprintf("%d bytes ditulis", len(content)), true, slashRel

	default:
		return "error: tool tidak dikenal", name, false, ""
	}
}

func buildToolSchemas(allowWrite bool) []map[string]any {
	tools := []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "list_files",
				"description": "Daftar file di dalam repository lokal",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"dir": map[string]any{"type": "string", "description": "Subfolder relatif (default '.')"},
					},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "read_file",
				"description": "Baca isi file teks di dalam repository",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Path relatif file"},
					},
					"required": []string{"path"},
				},
			},
		},
	}
	if allowWrite {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "write_file",
				"description": "Tulis atau ubah isi file di dalam repository",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "description": "Path relatif file"},
						"content": map[string]any{"type": "string", "description": "Isi file lengkap yang baru"},
					},
					"required": []string{"path", "content"},
				},
			},
		})
	}
	return tools
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
