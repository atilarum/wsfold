package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/atilarum/wsfold/internal/wsfold"
)

const zshCompletionScript = `
autoload -Uz compinit 2>/dev/null || true
if ! typeset -f compdef >/dev/null 2>&1; then
  compinit >/dev/null 2>&1
fi

_wsfold_executable() {
  if [[ -n "${words[1]:-}" && -x "${words[1]}" ]]; then
    printf '%s\n' "${words[1]}"
    return
  fi

  if command -v wsfold >/dev/null 2>&1; then
    printf '%s\n' "wsfold"
    return
  fi

  printf '%s\n' "wsfold"
}

_wsfold() {
  local context state
  typeset -A opt_args

  local -a subcommands
  subcommands=(
__WSFOLD_COMMANDS__
  )

  _arguments -C \
    '1:command:->command' \
    '2:repo ref:->repo' \
    '3::arg:->arg'

  case $state in
    command)
      _describe -V -t commands 'wsfold commands' subcommands
      return
      ;;
    repo)
      local cmd="${words[2]}"
      local current="${PREFIX}"
      local executable="$(_wsfold_executable)"
      local -a raw
      local -a described
      local candidate value description

      raw=("${(@f)$(${executable} __complete "${cmd}" "${current}" 2>/dev/null)}")
      for candidate in "${raw[@]}"; do
        value="${candidate%%$'\t'*}"
        if [[ "$candidate" == *$'\t'* ]]; then
          description="${candidate#*$'\t'}"
          described+=("${value}:${description}")
        else
          described+=("${value}")
        fi
      done

      _describe -t repo_refs 'repo refs' described
      return
      ;;
    arg)
      if [[ "${words[2]}" == "completion" ]]; then
        _describe -t shells 'shells' 'zsh:generate zsh completion script'
      fi
      return
      ;;
  esac
}

compdef _wsfold wsfold ./dist/wsfold ./wsfold
`

const zshCompletionSetupHelp = `Shell completion setup

Current shell session:
  eval "$(wsfold completion zsh)"

Persist in your zsh profile:
  echo 'eval "$(wsfold completion zsh)"' >> ~/.zshrc
  exec zsh
`

func writeZshCompletion(w io.Writer) error {
	script := strings.Replace(zshCompletionScript, "__WSFOLD_COMMANDS__", zshCompletionCommandEntries(), 1)
	_, err := io.WriteString(w, script)
	return err
}

func writeCompletionSetupHelp(w io.Writer) error {
	_, err := io.WriteString(w, zshCompletionSetupHelp)
	return err
}

func writeCompletions(cwd string, args []string, stdout io.Writer) error {
	if len(args) == 1 {
		return writeCompletionSetupHelp(stdout)
	}
	if len(args) != 2 {
		return fmt.Errorf("expected zero or one shell name, got %d arguments", len(args)-1)
	}

	switch args[1] {
	case "zsh":
		return writeZshCompletion(stdout)
	default:
		return fmt.Errorf("unsupported shell %q", args[1])
	}
}

func writeDynamicCompletions(cwd string, args []string, stdout io.Writer) error {
	if len(args) != 3 {
		return nil
	}

	app := wsfold.NewApp()
	command := args[1]
	if command == "remove-worktrees" {
		return nil
	}
	candidates, err := app.Complete(cwd, command, args[2])
	if err != nil {
		return err
	}

	for _, candidate := range candidates {
		if (command == "summon" || command == "summon-external") && candidate.Attached && candidate.Realization != wsfold.RealizationUnmounted {
			continue
		}
		line := candidate.Value
		if strings.TrimSpace(candidate.Description) != "" {
			line += "\t" + candidate.Description
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}

	return nil
}

func zshCompletionCommandEntries() string {
	lines := make([]string, 0, len(commandHelpEntries))
	for _, entry := range commandHelpEntries {
		lines = append(lines, fmt.Sprintf("    '%s:%s'", entry.Name, entry.Description))
	}
	return strings.Join(lines, "\n")
}
