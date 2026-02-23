package web

import "embed"

// PublicFS 内嵌管理页静态资源。
//
//go:embed public/* public/vendor/*
var PublicFS embed.FS
