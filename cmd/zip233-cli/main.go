// Command zip233-cli exposes the archive engine as a small command line tool.
// Passwords are never accepted as positional arguments; prefer
// --password-stdin or --ask-password over the visible --password flag.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/neko233-com/zip233/internal/archive"
	"github.com/neko233-com/zip233/internal/contract"
	"github.com/neko233-com/zip233/internal/version"
)

var (
	jsonOutput    bool
	flagPassword  string
	askPassword   bool
	passwordStdin bool
	flagOutput    string
	flagFormat    string
	flagLevel     string
	flagJobs      int
)

func main() {
	root := &cobra.Command{
		Use:           "zip233-cli",
		Short:         "zip233 归档命令行工具",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "以 JSON 输出结果")
	root.PersistentFlags().IntVar(&flagJobs, "jobs", 0, "并发任务数（默认 min(CPU 核数, 8)）")

	root.AddCommand(formatsCmd())
	root.AddCommand(listCmd())
	root.AddCommand(testCmd())
	root.AddCommand(extractCmd())
	root.AddCommand(createCmd())
	root.AddCommand(benchmarkCmd())
	root.AddCommand(splitCmd())
	root.AddCommand(joinCmd())

	if err := root.Execute(); err != nil {
		// In TUI mode the failure card has already been drawn; only the exit
		// code is left to main.
		if !tuiActive {
			if jsonOutput {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "error": err.Error()})
			} else {
				fmt.Fprintf(os.Stderr, "错误：%s\n", err)
			}
		}
		os.Exit(1)
	}
}

func addPasswordFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&flagPassword, "password", "", "归档密码（会显示在进程列表中，建议使用 --password-stdin）")
	cmd.Flags().BoolVar(&askPassword, "ask-password", false, "从终端交互式读取密码")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "从标准输入读取密码")
}

func resolvePassword() (string, error) {
	if passwordStdin {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("读取标准输入密码失败：%w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	if askPassword {
		fmt.Fprint(os.Stderr, "请输入归档密码：")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("读取终端密码失败：%w", err)
		}
		return string(b), nil
	}
	return flagPassword, nil
}

func printResult(result contract.ToolResult) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if result.Stdout != "" {
		fmt.Println(result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprintln(os.Stderr, result.Stderr)
	}
	if !result.OK {
		return fmt.Errorf("操作失败")
	}
	return nil
}

func formatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "formats",
		Short: "列出支持创建与读取的格式",
		RunE: func(cmd *cobra.Command, args []string) error {
			create := archive.SupportedCreateFormats()
			read := archive.SupportedReadFormats()
			if jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(map[string]any{"create": create, "read": read})
			}
			if useTUI() {
				return runTUI(tuiJob{
					command: "formats",
					target:  "归档格式支持矩阵",
					work: func() tuiOutcome {
						return tuiOutcome{lines: []string{
							tuiKV("可创建", strings.Join(create, " · ")),
							tuiKV("可读取", strings.Join(read, " · ")),
						}}
					},
				})
			}
			fmt.Println("可创建的格式：", strings.Join(create, ", "))
			fmt.Println("可读取的格式：", strings.Join(read, ", "))
			return nil
		},
	}
}

// formatItems renders an archive listing for both the plain text and TUI
// paths, keeping the two outputs identical in content.
func formatItems(items []contract.ArchiveItem) []string {
	lines := make([]string, 0, len(items)+1)
	for _, item := range items {
		kind := "文件"
		if item.IsDirectory {
			kind = "目录"
		}
		size := int64(0)
		if item.Size != nil {
			size = *item.Size
		}
		lines = append(lines, fmt.Sprintf("%-8s %12d  %s", kind, size, item.Name))
	}
	lines = append(lines, fmt.Sprintf("共 %d 个条目", len(items)))
	return lines
}

func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <archive>",
		Short: "列出压缩包内的条目",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			password, err := resolvePassword()
			if err != nil {
				return err
			}
			if jsonOutput {
				items, err := archive.List(args[0], password)
				if err != nil {
					return err
				}
				return json.NewEncoder(os.Stdout).Encode(map[string]any{"items": items, "count": len(items)})
			}
			if useTUI() {
				return runTUI(tuiJob{
					command: "list",
					target:  args[0],
					work: func() tuiOutcome {
						items, err := archive.List(args[0], password)
						if err != nil {
							return tuiOutcome{lines: []string{err.Error()}, failed: true, err: err}
						}
						return tuiOutcome{lines: formatItems(items)}
					},
				})
			}
			items, err := archive.List(args[0], password)
			if err != nil {
				return err
			}
			for _, line := range formatItems(items) {
				fmt.Println(line)
			}
			return nil
		},
	}
	addPasswordFlags(cmd)
	return cmd
}

func testCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test <archive>",
		Short: "校验压缩包完整性与密码",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			password, err := resolvePassword()
			if err != nil {
				return err
			}
			if useTUI() {
				return runArchiveTUI("test", args[0], func() (contract.ToolResult, error) {
					return archive.Test(args[0], password)
				})
			}
			result, err := archive.Test(args[0], password)
			if err != nil {
				return err
			}
			return printResult(result)
		},
	}
	addPasswordFlags(cmd)
	return cmd
}

func extractCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extract <archive>",
		Short: "解压压缩包到指定目录",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			password, err := resolvePassword()
			if err != nil {
				return err
			}
			if strings.TrimSpace(flagOutput) == "" {
				return fmt.Errorf("必须使用 --output 指定输出目录")
			}
			target := args[0] + "  →  " + flagOutput
			if useTUI() {
				return runArchiveTUI("extract", target, func() (contract.ToolResult, error) {
					return archive.Extract(args[0], flagOutput, password)
				})
			}
			result, err := archive.Extract(args[0], flagOutput, password)
			if err != nil {
				return err
			}
			return printResult(result)
		},
	}
	cmd.Flags().StringVar(&flagOutput, "output", "", "输出目录（必填）")
	addPasswordFlags(cmd)
	return cmd
}

func createCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <target> <paths...>",
		Short: "创建压缩包",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			password, err := resolvePassword()
			if err != nil {
				return err
			}
			target := fmt.Sprintf("%s  ←  %d 个源", args[0], len(args)-1)
			if useTUI() {
				return runArchiveTUI("create", target, func() (contract.ToolResult, error) {
					return archive.Create(args[1:], args[0], flagFormat, flagLevel, password)
				})
			}
			result, err := archive.Create(args[1:], args[0], flagFormat, flagLevel, password)
			if err != nil {
				return err
			}
			return printResult(result)
		},
	}
	cmd.Flags().StringVar(&flagFormat, "format", "", "目标格式：zip/7z/tar/tar.gz/tar.xz/tar.bz2/gz/bz2/xz（默认按扩展名推断）")
	cmd.Flags().StringVar(&flagLevel, "level", "", "压缩级别：none/fast/best 或 0-9")
	addPasswordFlags(cmd)
	return cmd
}
