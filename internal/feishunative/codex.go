package feishunative

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"suncodexclaw/internal/memory"
)

func DetectBinary(bin string, versionArg string) (bool, string) {
	bin = strings.TrimSpace(bin)
	if bin == "" {
		return false, ""
	}
	cmd := exec.Command(bin, versionArg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, ""
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return true, ""
	}
	line := strings.Split(text, "\n")[0]
	return true, strings.TrimSpace(line)
}

type codexProgressEvent map[string]any

type CodexRunRequest struct {
	Prompt          string
	FallbackPrompt  string
	ResumeSessionID string
	ImagePaths      []string
	OnEvent         func(codexProgressEvent)
}

type CodexReply struct {
	Reply    string
	ThreadID string
}

type codexExecutionPolicy struct {
	SandboxType  string
	ApprovalMode string
}

var cachedCodexStateDBPath string

var defaultTimerSystemGuide = strings.Join([]string{
	"定时任务系统：如果用户提到 /timer、定时、计划任务、周期执行，优先使用 `suncodexclawd timer ...` 管理内置 timer 系统。",
	"先用 `suncodexclawd timer list` 或 `suncodexclawd timer show <id>` 查看现状。",
	"创建或更新任务优先使用 `suncodexclawd timer upsert`。",
	"如果只是修改已有任务的一部分字段，例如 prompt、执行时间、工作目录或 chat_id，优先使用 `suncodexclawd timer update <id> ...`。",
	"常用方式：`--every 1h`、`--daily 09:00`、`--weekly mon,tue,fri --at 09:00`。",
	"如果用户没有指定发送目标，默认把 `--chat-id` 设为当前聊天的 chat_id。",
	"如果当前命令运行在机器人工作目录里，`timer` 可直接从 `.config.toml` 推断当前账号；不在工作目录里时再显式补 `--account <当前机器人账号>`。",
}, "\n")

var defaultMemorySystemGuide = strings.Join([]string{
	"记忆系统：如果用户提到 /memory、记住、保存偏好、回忆历史约定，优先使用 `suncodexclawd memory ...` 管理内置 memory 系统。",
	"当前机器人的记忆默认写入独立记忆库，适合同一套服务运行多个机器人账号。",
	"如果用户不确定 memory 命令格式，可提示先看飞书里的 `/memory help`。",
	"运行 Codex 前会自动检索相关长期记忆注入上下文；高价值长期规则可通过 `memory pin` 或 `memory update --priority/--kind` 主动提高命中概率。",
	"如果用户再次强调同一条或近似表达的长期偏好或规则，系统会在避免重复写入的同时累积强化次数，并温和提高它的权重。",
	"手动执行 `memory add` 时，如果命中高置信度重复，系统也会优先强化已有记忆，而不是继续创建近似重复条目。",
	"如果用户明确要求保留新的近似重复记忆，可使用命令行 `memory add --force-new` 或 `memory force`；在飞书里可使用 `/memory add --force-new ...` 或 `/memory force ...`。",
	"如果用户要删除一条错误或低价值记忆，命令行优先使用 `memory delete <id>`；飞书里可使用 `/memory delete <id>` 或 `/memory remove <id>`。",
	"需要填写 `--account` 时，优先使用当前提示里明确给出的“当前机器人账号”；多机器人场景下，以当前加载的 `config/feishu/bots.toml` 中对应机器人表和启动参数 `--account <account>` 为准；如果账号名里有点号或空格，手工编辑 TOML 时记得给表名加引号。",
	"添加记忆使用 `suncodexclawd memory add --account <当前机器人账号> --text \"...\"`，检索使用 `suncodexclawd memory search <关键词> --account <当前机器人账号>`，总览使用 `suncodexclawd memory stats --account <当前机器人账号>`，召回预览使用 `suncodexclawd memory recall <关键词> --account <当前机器人账号>`，治理体检使用 `suncodexclawd memory review --account <当前机器人账号>`，确认规则稳定后可用 `memory review --apply-promote|--apply-stale|--apply-all` 批量执行建议；点查某条记忆附近候选使用 `suncodexclawd memory related <id> --account <当前机器人账号>`，更新权重使用 `suncodexclawd memory update <id> --priority 80 --kind preference --pinned`，低价值旧记忆优先用 `suncodexclawd memory archive <id>` 暂时下线，需要时再 `unarchive`，长期归档后再用 `suncodexclawd memory purge --days 30` 预览、确认后 `--apply` 物理清理；历史重复条目可先用 `suncodexclawd memory duplicates` 查看，再用 `suncodexclawd memory merge <keep-id> <drop-id>...` 或 `suncodexclawd memory dedupe --apply` 收敛。",
	"如果当前命令运行在机器人工作目录里，也可以直接执行 `suncodexclawd memory list|search ...`，账号会从 `.config.toml` 自动识别。",
	"优先先用 `suncodexclawd memory search <关键词> --account <当前机器人账号>` 或 `suncodexclawd memory list --account <当前机器人账号>` 查看已有记忆；即使直接 add，系统也会保守处理高置信度重复。",
	"如果用户要求“记住这件事”，优先把明确、长期有效的偏好或规则写进 memory。",
}, "\n")

var defaultSyncSystemGuide = strings.Join([]string{
	"文档同步系统：如果用户提到 /sync、备份、恢复工作区文档、WebDAV，同样优先使用 `suncodexclawd sync ...` 管理内置同步系统。",
	"先用 `suncodexclawd sync status` 或 `suncodexclawd sync list-remote` 查看当前状态和远端快照。",
	"上传当前工作区核心文档使用 `suncodexclawd sync push`。",
	"恢复远端文档优先区分 `sync pull` 和 `sync restore`：`pull` 拉到恢复目录，`restore` 用于补齐或恢复工作区文档。",
	"如果当前命令运行在机器人工作目录里，`sync` 可直接从 `.config.toml` 推断当前账号；不在工作目录里时再显式补 `--account <当前机器人账号>`。",
}, "\n")

var defaultEnvSystemGuide = strings.Join([]string{
	"环境变量库：如果用户需要为当前机器人保存 token、key、endpoint、cookie 或其他敏感配置，优先使用 `suncodexclawd env ...`，不要把敏感值写进普通回复、memory、timer prompt 或工作区文档。",
	"默认优先使用账号作用域：`suncodexclawd env set --account <当前机器人账号> --key NAME --value '...'`；只有明确要求跨机器人共用时才使用 `--scope global`。",
	"读取时优先用 `suncodexclawd env get --account <当前机器人账号> NAME`；该命令默认脱敏，不会打印明文。",
	"如果你需要把密钥传给其他命令，优先使用 `suncodexclawd env run --account <当前机器人账号> --key NAME -- <command> ...`，避免明文出现在终端输出里。",
	"只有在 absolutely necessary 且后续不会回显的情况下，才使用 `suncodexclawd env get --raw ...`；拿到后不要在回复里复述这个值。",
}, "\n")

var defaultClawHubSystemGuide = strings.Join([]string{
	"技能检索：如果用户提到 skill、skills、技能、ClawHub、提示词模板、Codex 技能，优先使用 `suncodexclawd clawhub ...` 检索公开技能，不要凭记忆编造技能名称或内容。",
	"先用 `suncodexclawd clawhub search <关键词>` 找候选，再用 `suncodexclawd clawhub show <skill-slug>` 看元数据。",
	"如果需要读取技能正文或安装说明，优先使用 `suncodexclawd clawhub file <skill-slug> --path SKILL.md`；需要其他文件时再把 `--path` 换成对应相对路径。",
	"如果用户只是想浏览热门或最近更新的技能，可用 `suncodexclawd clawhub list --sort updated` 或其他排序方式。",
}, "\n")

func RunCodex(ctx context.Context, cfg CodexConfig, request CodexRunRequest) (CodexReply, error) {
	prompt := strings.TrimSpace(request.Prompt)
	fallbackPrompt := strings.TrimSpace(request.FallbackPrompt)
	tempDir, err := os.MkdirTemp("", "suncodexclaw-codex-")
	if err != nil {
		return CodexReply{}, err
	}
	defer os.RemoveAll(tempDir)

	outputFile := filepath.Join(tempDir, "last-message.txt")
	resumeID := strings.TrimSpace(request.ResumeSessionID)
	if resumeID != "" {
		existingPolicy := readCodexThreadExecutionPolicy(resumeID)
		expectedSandbox := strings.TrimSpace(cfg.Sandbox)
		expectedApproval := strings.TrimSpace(cfg.ApprovalPolicy)
		if existingPolicy == nil ||
			(expectedSandbox != "" && existingPolicy.SandboxType != "" && existingPolicy.SandboxType != expectedSandbox) ||
			(expectedApproval != "" && existingPolicy.ApprovalMode != "" && existingPolicy.ApprovalMode != expectedApproval) {
			fmt.Printf("codex_resume_skip thread_id=%s reason=policy_mismatch existing_sandbox=%s existing_approval=%s expected_sandbox=%s expected_approval=%s\n",
				resumeID,
				emptyFallback(policySandbox(existingPolicy), "(unknown)"),
				emptyFallback(policyApproval(existingPolicy), "(unknown)"),
				emptyFallback(expectedSandbox, "(none)"),
				emptyFallback(expectedApproval, "(none)"),
			)
			resumeID = ""
			if fallbackPrompt != "" {
				prompt = fallbackPrompt
			}
		}
	}
	reply, err := runCodexExecOnce(ctx, cfg, prompt, request.ImagePaths, resumeID, outputFile, request.OnEvent)
	if err != nil && resumeID != "" {
		fmt.Printf("codex_resume=error thread_id=%s message=%s\n", resumeID, compactText(err.Error(), 400))
		freshPrompt := fallbackPrompt
		if freshPrompt == "" {
			freshPrompt = prompt
		}
		return runCodexExecOnce(ctx, cfg, freshPrompt, request.ImagePaths, "", outputFile, request.OnEvent)
	}
	return reply, err
}

func runCodexExecOnce(ctx context.Context, cfg CodexConfig, prompt string, imagePaths []string, resumeID string, outputFile string, onEvent func(codexProgressEvent)) (CodexReply, error) {
	args := buildCodexExecArgs(cfg, outputFile, imagePaths, resumeID)
	cmd := exec.CommandContext(ctx, cfg.Bin, args...)
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}
	childEnv := os.Environ()
	if strings.TrimSpace(cfg.APIKey) != "" {
		childEnv = append(childEnv, "OPENAI_API_KEY="+cfg.APIKey, "CODEX_API_KEY="+cfg.APIKey)
	}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		childEnv = append(childEnv, "OPENAI_BASE_URL="+cfg.BaseURL, "OPENAI_API_BASE="+cfg.BaseURL)
	}
	cmd.Env = childEnv
	cmd.Stdin = strings.NewReader(strings.TrimSpace(prompt))
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return CodexReply{}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return CodexReply{}, err
	}
	if err := cmd.Start(); err != nil {
		return CodexReply{}, fmt.Errorf("codex exec failed: %s", compactText(err.Error(), 1200))
	}

	var wg sync.WaitGroup
	var stdoutRaw strings.Builder
	var stderrRaw strings.Builder
	var stdoutMu sync.Mutex
	var stderrMu sync.Mutex
	observedThreadID := resumeID

	wg.Add(1)
	go func() {
		defer wg.Done()
		readCodexJSONL(stdoutPipe, func(line string, event codexProgressEvent) {
			stdoutMu.Lock()
			stdoutRaw.WriteString(line)
			stdoutRaw.WriteString("\n")
			raw := stdoutRaw.String()
			if len(raw) > 4000 {
				raw = raw[len(raw)-4000:]
				stdoutRaw.Reset()
				stdoutRaw.WriteString(raw)
			}
			stdoutMu.Unlock()
			if eventString(event, "type") == "thread.started" {
				if threadID := eventString(event, "thread_id"); threadID != "" {
					observedThreadID = threadID
				}
			}
			if onEvent != nil {
				onEvent(event)
			}
		}, func(line string) {
			stdoutMu.Lock()
			stdoutRaw.WriteString(line)
			stdoutRaw.WriteString("\n")
			raw := stdoutRaw.String()
			if len(raw) > 4000 {
				raw = raw[len(raw)-4000:]
				stdoutRaw.Reset()
				stdoutRaw.WriteString(raw)
			}
			stdoutMu.Unlock()
			if onEvent != nil {
				onEvent(codexProgressEvent{"type": "raw", "text": strings.TrimSpace(line)})
			}
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		readPipeLines(stderrPipe, func(line string) {
			stderrMu.Lock()
			stderrRaw.WriteString(line)
			stderrRaw.WriteString("\n")
			raw := stderrRaw.String()
			if len(raw) > 4000 {
				raw = raw[len(raw)-4000:]
				stderrRaw.Reset()
				stderrRaw.WriteString(raw)
			}
			stderrMu.Unlock()
		})
	}()

	err = cmd.Wait()
	wg.Wait()
	if err != nil {
		if ctx.Err() != nil {
			return CodexReply{}, ctx.Err()
		}
		stderrText := strings.TrimSpace(stderrRaw.String())
		stdoutText := strings.TrimSpace(stdoutRaw.String())
		text := stderrText
		if text == "" {
			text = stdoutText
		}
		if text == "" {
			text = err.Error()
		}
		return CodexReply{}, fmt.Errorf("codex exec failed: %s", compactText(text, 1200))
	}
	body, err := os.ReadFile(outputFile)
	if err != nil {
		return CodexReply{}, fmt.Errorf("read codex output failed: %w", err)
	}
	reply := strings.ReplaceAll(string(body), "\r", "")
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return CodexReply{}, fmt.Errorf("codex returned empty reply")
	}
	return CodexReply{
		Reply:    reply,
		ThreadID: strings.TrimSpace(observedThreadID),
	}, nil
}

func buildCodexExecArgs(cfg CodexConfig, outputFile string, imagePaths []string, resumeID string) []string {
	bypass := shouldBypassSandbox(cfg.Sandbox, cfg.ApprovalPolicy)
	args := []string{"exec"}
	if strings.TrimSpace(resumeID) != "" {
		args = append(args, "resume", "--skip-git-repo-check", "--json")
	} else {
		args = append(args, "--skip-git-repo-check", "--json")
	}
	if bypass {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}
	if cfg.ReasoningEffort != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", cfg.ReasoningEffort))
	}
	if strings.TrimSpace(resumeID) == "" && cfg.Profile != "" {
		args = append(args, "-p", cfg.Profile)
	}
	if strings.TrimSpace(resumeID) == "" && cfg.Cwd != "" {
		args = append(args, "-C", cfg.Cwd)
	}
	if strings.TrimSpace(resumeID) == "" {
		for _, dir := range cfg.AddDirs {
			if strings.TrimSpace(dir) == "" {
				continue
			}
			args = append(args, "--add-dir", dir)
		}
	}
	if strings.TrimSpace(resumeID) == "" && cfg.Sandbox != "" && !bypass {
		args = append(args, "-s", cfg.Sandbox)
	}
	if cfg.ApprovalPolicy != "" && !bypass {
		args = append(args, "-c", fmt.Sprintf("approval_policy=%q", cfg.ApprovalPolicy))
	}
	for _, imagePath := range imagePaths {
		if strings.TrimSpace(imagePath) == "" {
			continue
		}
		args = append(args, "-i", imagePath)
	}
	args = append(args, "--output-last-message", outputFile)
	if strings.TrimSpace(resumeID) != "" {
		args = append(args, resumeID)
	}
	args = append(args, "-")
	return args
}

func readCodexJSONL(r io.Reader, onEvent func(line string, event codexProgressEvent), onRaw func(line string)) {
	scanner := bufio.NewScanner(r)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event codexProgressEvent
		if err := json.Unmarshal([]byte(line), &event); err == nil {
			onEvent(line, event)
			continue
		}
		onRaw(line)
	}
}

func readPipeLines(r io.Reader, onLine func(line string)) {
	scanner := bufio.NewScanner(r)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		onLine(line)
	}
}

func buildPrompt(cfg Config, chatID, userText, title string, history []historyEntry, memories []memory.Entry) string {
	lines := []string{}
	if strings.TrimSpace(title) != "" {
		lines = append(lines, strings.TrimSpace(title), "")
	}
	lines = append(lines,
		strings.TrimSpace(cfg.Codex.SystemPrompt),
		"",
		defaultTimerSystemGuide,
		"",
		defaultMemorySystemGuide,
		"",
		defaultSyncSystemGuide,
		"",
		defaultEnvSystemGuide,
		"",
		defaultClawHubSystemGuide,
		"",
		"当前机器人账号："+cfg.AccountName,
		"当前聊天 chat_id："+emptyFallback(chatID, "(unknown)"),
		"当前工作目录："+emptyFallback(cfg.Codex.Cwd, emptyFallback(cfg.RepoRoot, ".")),
		"",
		"相关长期记忆：",
	)
	lines = append(lines, renderPromptMemories(memories)...)
	lines = append(lines,
		"",
		"对话上下文（按时间顺序，可能为空）：",
	)
	if len(history) == 0 {
		lines = append(lines, "(无)")
	} else {
		for _, item := range history {
			roleLabel := "用户"
			if item.Role == "assistant" {
				roleLabel = "助手"
			}
			lines = append(lines, "["+roleLabel+"] "+compactText(item.Text, 1200))
		}
	}
	lines = append(lines,
		"",
		"用户最新消息：",
		strings.TrimSpace(userText),
		"",
		"请直接输出给用户的最终回复正文，不要加“好的/收到”等空话，不要复述用户原文。",
		"禁止输出“稍后回复/几分钟后回复/晚点再回复”这类承诺。",
		"如果 SSH、curl、nc 或其他网络命令失败，不要直接归因于“当前会话不能联网”或“网络策略拦截”。先报告原始报错，再做更小的连通性探测复核。",
		fmt.Sprintf("如果你需要机器人把本机图片直接发给用户，请在回复中单独占行输出：%s/绝对或相对路径]]", feishuSendImageDirectivePrefix),
		fmt.Sprintf("如果你需要机器人把本机文件直接发给用户，请在回复中单独占行输出：%s/绝对或相对路径]]", feishuSendFileDirectivePrefix),
		"可以输出多行附件指令；除这些指令外，其他文字都会作为正常回复发送给用户。",
		fmt.Sprintf("发送图片前请确认文件真实存在、格式受支持，且大小不超过 %s。", formatBytes(feishuImageUploadLimit)),
		fmt.Sprintf("发送文件前请确认文件真实存在、不是目录，且大小不超过 %s。", formatBytes(feishuFileUploadLimit)),
		"如果用户发送了文件或图片，消息正文里会给出本地临时文件路径；需要时请直接读取对应文件。",
	)
	return strings.Join(lines, "\n")
}

func buildResumePrompt(cfg Config, chatID, userText string, imageCount int, memories []memory.Entry) string {
	lines := []string{
		"继续当前线程。下面是用户最新消息，请直接回复用户。",
		"",
		defaultTimerSystemGuide,
		"",
		defaultMemorySystemGuide,
		"",
		defaultSyncSystemGuide,
		"",
		defaultEnvSystemGuide,
		"",
		defaultClawHubSystemGuide,
		"",
		"当前机器人账号：" + cfg.AccountName,
		"当前聊天 chat_id：" + emptyFallback(chatID, "(unknown)"),
		"",
		"相关长期记忆：",
	}
	lines = append(lines, renderPromptMemories(memories)...)
	lines = append(lines,
		"",
		"用户最新消息：",
		strings.TrimSpace(userText),
	)
	if imageCount > 0 {
		lines = append(lines, fmt.Sprintf("附加图片：%d 张（请结合图片内容回答）。", imageCount))
	}
	lines = append(lines,
		"",
		"请直接输出给用户的最终回复正文，不要加“好的/收到”等空话，不要复述用户原文。",
		"禁止输出“稍后回复/几分钟后回复/晚点再回复”这类承诺。",
		"如果 SSH、curl、nc 或其他网络命令失败，不要直接归因于“当前会话不能联网”或“网络策略拦截”。先报告原始报错，再做更小的连通性探测复核。",
		fmt.Sprintf("如果你需要机器人把本机图片直接发给用户，请在回复中单独占行输出：%s/绝对或相对路径]]", feishuSendImageDirectivePrefix),
		fmt.Sprintf("如果你需要机器人把本机文件直接发给用户，请在回复中单独占行输出：%s/绝对或相对路径]]", feishuSendFileDirectivePrefix),
		"可以输出多行附件指令；除这些指令外，其他文字都会作为正常回复发送给用户。",
	)
	return strings.Join(lines, "\n")
}

func renderPromptMemories(memories []memory.Entry) []string {
	if len(memories) == 0 {
		return []string{"(无)"}
	}
	lines := make([]string, 0, len(memories)*2)
	for _, item := range memories {
		header := []string{"- " + emptyFallback(strings.TrimSpace(item.ID), "(no-id)")}
		if strings.TrimSpace(item.Kind) != "" {
			header = append(header, "kind="+strings.TrimSpace(item.Kind))
		}
		if item.Priority > 0 {
			header = append(header, fmt.Sprintf("priority=%d", item.Priority))
		}
		if item.Pinned {
			header = append(header, "pinned=true")
		}
		if item.UseCount > 0 {
			header = append(header, fmt.Sprintf("use_count=%d", item.UseCount))
		}
		if item.ReinforceCount > 0 {
			header = append(header, fmt.Sprintf("reinforce_count=%d", item.ReinforceCount))
		}
		if strings.TrimSpace(item.Source) != "" {
			header = append(header, "source="+strings.TrimSpace(item.Source))
		}
		if len(item.Tags) > 0 {
			header = append(header, "tags="+strings.Join(item.Tags, ","))
		}
		lines = append(lines, strings.Join(header, " | "))
		lines = append(lines, "  "+compactText(item.Text, 300))
	}
	return lines
}

func shouldBypassSandbox(sandbox, approval string) bool {
	return strings.TrimSpace(sandbox) == "danger-full-access" && strings.TrimSpace(approval) == "never"
}

func compactText(value string, max int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func eventString(event codexProgressEvent, key string) string {
	if event == nil {
		return ""
	}
	value, ok := event[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func resolveCodexStateDBPath() string {
	if cachedCodexStateDBPath != "" {
		if _, err := os.Stat(cachedCodexStateDBPath); err == nil {
			return cachedCodexStateDBPath
		}
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		codexHome = filepath.Join(home, ".codex")
	}
	entries, err := os.ReadDir(codexHome)
	if err != nil {
		return ""
	}
	candidates := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "state_") && strings.HasSuffix(name, ".sqlite") {
			candidates = append(candidates, filepath.Join(codexHome, name))
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, leftErr := os.Stat(candidates[i])
		right, rightErr := os.Stat(candidates[j])
		if leftErr != nil || rightErr != nil {
			return candidates[i] < candidates[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	cachedCodexStateDBPath = candidates[0]
	return cachedCodexStateDBPath
}

func quoteSQLiteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func syncCodexThreadTitle(threadID, threadTitle string) bool {
	targetThreadID := strings.TrimSpace(threadID)
	nextTitle := strings.TrimSpace(threadTitle)
	if targetThreadID == "" || nextTitle == "" {
		return false
	}
	dbPath := resolveCodexStateDBPath()
	if dbPath == "" {
		return false
	}
	sql := strings.Join([]string{
		"UPDATE threads",
		"SET title = " + quoteSQLiteString(nextTitle) + ",",
		"    updated_at = CAST(strftime('%s','now') AS INTEGER)",
		"WHERE id = " + quoteSQLiteString(targetThreadID) + ";",
	}, " ")
	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("codex_thread_title_sync=error thread_id=%s message=%s\n", targetThreadID, compactText(strings.TrimSpace(string(out)), 400))
		return false
	}
	return true
}

func readCodexThreadExecutionPolicy(threadID string) *codexExecutionPolicy {
	targetThreadID := strings.TrimSpace(threadID)
	if targetThreadID == "" {
		return nil
	}
	dbPath := resolveCodexStateDBPath()
	if dbPath == "" {
		return nil
	}
	sql := strings.Join([]string{
		"SELECT sandbox_policy, approval_mode",
		"FROM threads",
		"WHERE id = " + quoteSQLiteString(targetThreadID),
		"LIMIT 1;",
	}, " ")
	cmd := exec.Command("sqlite3", "-json", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("codex_thread_policy=error thread_id=%s message=%s\n", targetThreadID, compactText(strings.TrimSpace(string(out)), 400))
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) == 0 {
		return nil
	}
	row := rows[0]
	policy := &codexExecutionPolicy{
		ApprovalMode: strings.TrimSpace(fmt.Sprint(row["approval_mode"])),
	}
	switch value := row["sandbox_policy"].(type) {
	case string:
		var parsed map[string]any
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			policy.SandboxType = strings.TrimSpace(fmt.Sprint(parsed["type"]))
		}
	case map[string]any:
		policy.SandboxType = strings.TrimSpace(fmt.Sprint(value["type"]))
	}
	return policy
}

func policySandbox(policy *codexExecutionPolicy) string {
	if policy == nil {
		return ""
	}
	return strings.TrimSpace(policy.SandboxType)
}

func policyApproval(policy *codexExecutionPolicy) string {
	if policy == nil {
		return ""
	}
	return strings.TrimSpace(policy.ApprovalMode)
}
