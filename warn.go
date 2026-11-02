package discord

import (
	"fmt"
	"os"
)

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "discord gateway: "+format+"\n", args...)
}
