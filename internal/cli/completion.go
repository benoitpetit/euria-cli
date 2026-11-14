package cli

import (
	"fmt"
	"io"
)

func runCompletion(opts Options, stdout io.Writer) error {
	switch opts.Subcommand {
	case "bash":
		_, err := io.WriteString(stdout, bashCompletion)
		return err
	case "zsh":
		_, err := io.WriteString(stdout, zshCompletion)
		return err
	case "fish":
		_, err := io.WriteString(stdout, fishCompletion)
		return err
	default:
		return fmt.Errorf("unsupported shell %q; choose bash, zsh, or fish", opts.Subcommand)
	}
}

const bashCompletion = `# euria bash completion
_euria() {
    local cur prev words cword
    _init_completion || return
    local commands="auth conversations conversation completion"
    local subcommands="login status list search show rename archive unarchive favorite unfavorite share bash zsh fish"
    if [[ $cword -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
        return
    fi
    COMPREPLY=( $(compgen -W "$subcommands --chat --guest --model --web-search --history --output --thinking --project --tool --browser --profile --chrome-profile --account --conversation --ephemeral --stream --audio --voice --language --archived --favorites --login-timeout --list-models --help" -- "$cur") )
}
complete -F _euria euria
`

const zshCompletion = `#compdef euria
_euria() {
    local -a commands
    commands=(
        'auth:authentication commands'
        'conversations:list or search conversations'
        'conversation:manage a conversation'
        'completion:generate shell completion'
    )
    _describe 'command' commands
}
compdef _euria euria
`

const fishCompletion = `# euria fish completion
complete -c euria -f
complete -c euria -n '__fish_use_subcommand' -a 'auth conversations conversation completion'
`
