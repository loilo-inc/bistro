package ldd

// Dynamic String Tokens ($LIB and $PLATFORM) are fixed per architecture
// instead of being queried from the host's ld.so. This is a deliberate
// trade-off:
//   - The bundled rootfs runs on the deployment host, not on the build host,
//     so the build host's values are not authoritative anyway.
//   - Debian packages rarely use these tokens in DT_RPATH, DT_RUNPATH, or
//     DT_NEEDED.
//
// Unsupported architectures get empty tokens rather than an error. A path
// that depends on a token then fails to resolve and is reported as a broken
// library.

// getDstLib returns $LIB, which is the multiarch library directory on Debian.
func getDstLib(arch string) string {
	switch arch {
	case "amd64":
		return "lib/x86_64-linux-gnu"
	case "arm64":
		return "lib/aarch64-linux-gnu"
	}
	return ""
}

// getDstPlatform returns $PLATFORM as the kernel reports it (AT_PLATFORM).
// glibc may override it on x86_64 with CPU-specific values such as "haswell"
// or "xeon_phi", which are not reproduced here.
func getDstPlatform(arch string) string {
	switch arch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return ""
}
