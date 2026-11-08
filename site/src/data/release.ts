// The one switch to flip at launch. While `available` is false the install
// section says binaries are on the way and shows the from-source path; once
// it is true it offers downloads, preselecting the visitor's platform.
//
// Asset names follow GoReleaser's default archive template. GitHub serves
// `/releases/latest/download/<name>` for the newest release, so these links
// keep working across versions without editing this file.

export interface ReleaseAsset {
  os: "macos" | "linux";
  arch: "arm64" | "amd64";
  label: string;
  file: string;
}

export const release = {
  available: false,
  /** The tag of the newest release without its "v" (say "0.1.0"), shown
   *  beside the downloads so a visitor can check what they get. Leave empty
   *  to show only the license, platforms and release notes. */
  version: "",
  repo: "https://github.com/YunusAndreasson/safina",
  fromSource: "git clone https://github.com/YunusAndreasson/safina && cd safina && make install",
  assets: [
    { os: "macos", arch: "arm64", label: "macOS, Apple silicon", file: "safina_darwin_arm64.tar.gz" },
    { os: "macos", arch: "amd64", label: "macOS, Intel", file: "safina_darwin_amd64.tar.gz" },
    { os: "linux", arch: "amd64", label: "Linux, x86-64", file: "safina_linux_amd64.tar.gz" },
    { os: "linux", arch: "arm64", label: "Linux, ARM64", file: "safina_linux_arm64.tar.gz" },
  ] satisfies ReleaseAsset[],
};

export const downloadUrl = (file: string) => `${release.repo}/releases/latest/download/${file}`;
