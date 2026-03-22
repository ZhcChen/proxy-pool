//go:build !mihomo_bundle

package server

func bundledMihomoAsset(osName, arch string) (MihomoLatest, []byte, string, bool) {
	_ = osName
	_ = arch
	return MihomoLatest{}, nil, "", false
}
