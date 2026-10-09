package main

import (
	"go.uber.org/fx"

	"junglegaming.io/finops-api/internal/platform/fxapp"
)

func main() {
	fx.New(fxapp.Module()).Run()
}
