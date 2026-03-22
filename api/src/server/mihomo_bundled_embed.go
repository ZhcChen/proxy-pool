//go:build mihomo_bundle

package server

import _ "embed"

var (
	bundledMihomoTag       = "embedded"
	bundledMihomoAssetName = "mihomo-linux-amd64.gz"
	bundledMihomoSHA256    = ""
	bundledMihomoSourceURL = "embedded://mihomo/linux-amd64"
)

//go:embed assets/mihomo/mihomo-linux-amd64.gz
var bundledMihomoLinuxAMD64GZ []byte

func bundledMihomoAsset(osName, arch string) (MihomoLatest, []byte, string, bool) {
	if osName == "linux" && arch == "amd64" && len(bundledMihomoLinuxAMD64GZ) > 0 {
		return MihomoLatest{
				Tag:         bundledMihomoTag,
				Prerelease:  false,
				PublishedAt: "",
				AssetName:   bundledMihomoAssetName,
				DownloadURL: bundledMihomoSourceURL,
			},
			bundledMihomoLinuxAMD64GZ,
			bundledMihomoSHA256,
			true
	}
	return MihomoLatest{}, nil, "", false
}
