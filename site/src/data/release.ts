// The install section's facts. While `available` is false it says binaries
// are on the way and shows only the from-source path; once it is true it
// leads with the one-line install, lists the archives (the visitor's
// platform first) and keeps from-source for everything else.
//
// The archives are what .github/workflows/release.yml publishes for a `v*`
// tag, and public/install.sh downloads the same names. GitHub serves
// `/releases/latest/download/<name>` for the newest release, so these links
// keep working across versions without editing this file; bump `version` with
// each release so the page names what it serves.

export interface ReleaseAsset {
  os: "macos" | "linux";
  arch: "arm64" | "amd64";
  label: string;
  file: string;
}

export const release = {
  available: true,
  /** The tag of the newest release without its "v" (say "0.1.0"), shown
   *  beside the downloads so a visitor can check what they get. Leave empty
   *  to show only the license, platforms and release notes. */
  version: "0.3.0",
  repo: "https://github.com/YunusAndreasson/safina",
  /** Linux only, for now: install.sh refuses anything else with the from-source line. */
  install: "curl -fsSL https://safina.run/install.sh | sh",
  fromSource: "git clone https://github.com/YunusAndreasson/safina && cd safina && make install",
  assets: [
    { os: "linux", arch: "amd64", label: "Linux, x86-64", file: "safina_linux_amd64.tar.gz" },
    { os: "linux", arch: "arm64", label: "Linux, ARM64", file: "safina_linux_arm64.tar.gz" },
  ] satisfies ReleaseAsset[],
};

export const downloadUrl = (file: string) => `${release.repo}/releases/latest/download/${file}`;
