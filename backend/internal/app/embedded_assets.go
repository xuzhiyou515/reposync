package app

import "embed"

//go:embed embedded/*
var embeddedFrontend embed.FS
