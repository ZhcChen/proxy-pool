package server

import "testing"

func TestBundledMihomoAsset_LinuxAMD64Available(t *testing.T) {
	latest, buf, sha, ok := bundledMihomoAsset("linux", "amd64")
	if !ok {
		if len(buf) != 0 || sha != "" || latest.Tag != "" || latest.AssetName != "" || latest.DownloadURL != "" {
			t.Fatalf("无内置资源时返回值异常: latest=%+v len(buf)=%d sha=%q", latest, len(buf), sha)
		}
		return
	}
	if latest.Tag == "" || latest.AssetName == "" || latest.DownloadURL == "" {
		t.Fatalf("内置资源元数据不完整: %+v", latest)
	}
	if len(buf) == 0 {
		t.Fatal("内置资源为空")
	}
	if sha == "" {
		t.Fatal("内置资源缺少 sha256")
	}
	if err := verifySHA256(buf, sha); err != nil {
		t.Fatalf("内置资源 sha256 校验失败: %v", err)
	}
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("abc")
	if err := verifySHA256(data, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"); err != nil {
		t.Fatalf("sha256 正常值校验失败: %v", err)
	}
	if err := verifySHA256(data, "deadbeef"); err == nil {
		t.Fatal("期望 sha256 错误值返回失败")
	}
}
