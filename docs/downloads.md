# Download DVFS Client

Download the official standalone executable binary for the **Distributed Virtual File System (DVFS) Client**.

---

<div id="os-detect-banner" style="display:none; margin: 1.5rem 0; padding: 1.5rem; border-radius: 12px; background: linear-gradient(135deg, rgba(63,81,181,0.1) 0%, rgba(13,202,240,0.1) 100%); border: 2px solid #3f51b5;">
  <div style="display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 1rem;">
    <div>
      <span style="font-size: 0.85rem; text-transform: uppercase; font-weight: bold; letter-spacing: 0.5px; color: #3f51b5;">Detected Operating System</span>
      <h2 id="detected-os-title" style="margin: 0.2rem 0; border: none; font-size: 1.6rem; font-weight: bold;">DVFS Client Executable</h2>
      <p id="detected-os-desc" style="margin: 0; color: #555; font-size: 0.95rem;">Standalone binary.</p>
    </div>
    <div>
      <a id="detected-os-btn" href="https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest" class="md-button md-button--primary" style="font-size: 1rem; padding: 0.6rem 1.6rem; border-radius: 8px; text-decoration: none; font-weight: bold; display: inline-flex; align-items: center; gap: 8px;">
        <span id="detected-os-btn-text">Download Executable Binary</span>
      </a>
    </div>
  </div>
</div>

<script>
(function() {
  var ua = window.navigator.userAgent || "";
  var platform = window.navigator.platform || "";
  var banner = document.getElementById("os-detect-banner");
  var title = document.getElementById("detected-os-title");
  var desc = document.getElementById("detected-os-desc");
  var btn = document.getElementById("detected-os-btn");
  var btnText = document.getElementById("detected-os-btn-text");

  var baseUrl = "https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/";

  if (banner && title && desc && btn && btnText) {
    banner.style.display = "block";

    if (ua.indexOf("Win") !== -1 || platform.indexOf("Win") !== -1) {
      title.innerText = "DVFS Client for Windows (x86_64)";
      desc.innerText = "Single standalone .exe binary with embedded Root CA and Google PKCE auth.";
      btn.href = baseUrl + "dvfs-client-windows-amd64.exe";
      btnText.innerText = "Download dvfs-client-windows-amd64.exe";
    } else if (ua.indexOf("Mac") !== -1 || platform.indexOf("Mac") !== -1) {
      var isArm = /Macintosh/i.test(ua) && navigator.maxTouchPoints && navigator.maxTouchPoints > 2;
      if (ua.indexOf("arm64") !== -1 || isArm) {
        title.innerText = "DVFS Client for macOS (Apple Silicon M1/M2/M3/M4)";
        desc.innerText = "Native standalone ARM64 binary for Apple Silicon.";
        btn.href = baseUrl + "dvfs-client-darwin-arm64";
        btnText.innerText = "Download dvfs-client-darwin-arm64";
      } else {
        title.innerText = "DVFS Client for macOS (Intel x86_64)";
        desc.innerText = "Native standalone 64-bit binary for Intel Macs.";
        btn.href = baseUrl + "dvfs-client-darwin-amd64";
        btnText.innerText = "Download dvfs-client-darwin-amd64";
      }
    } else if (ua.indexOf("Linux") !== -1 || platform.indexOf("Linux") !== -1) {
      if (ua.indexOf("aarch64") !== -1 || ua.indexOf("arm64") !== -1) {
        title.innerText = "DVFS Client for Linux (ARM64 / Raspberry Pi)";
        desc.innerText = "Standalone ARM64 binary for Raspberry Pi 4/5 and ARM64 servers.";
        btn.href = baseUrl + "dvfs-client-linux-arm64";
        btnText.innerText = "Download dvfs-client-linux-arm64";
      } else {
        title.innerText = "DVFS Client for Linux (x86_64 / AMD64)";
        desc.innerText = "Statically linked standalone binary for Linux x86_64.";
        btn.href = baseUrl + "dvfs-client-linux-amd64";
        btnText.innerText = "Download dvfs-client-linux-amd64";
      }
    } else {
      title.innerText = "DVFS Client Binaries";
      desc.innerText = "Download the standalone executable for your operating system.";
      btn.href = "https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest";
      btnText.innerText = "View All Releases";
    }
  }
})();
</script>

---

## 1. Direct Binary Downloads by Operating System

Select your operating system to download the standalone executable binary directly:

=== "Windows"

    ### Windows (64-bit AMD64)

    | Edition | Binary File | Direct Download Link |
    | :--- | :--- | :--- |
    | **Authenticated (Google Auth)** | `dvfs-client-windows-amd64.exe` | [**Download dvfs-client-windows-amd64.exe**](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-windows-amd64.exe){ .md-button .md-button--primary } |
    | **Offline / No-Auth** | `dvfs-client-no-auth-windows-amd64.exe` | [Download dvfs-client-no-auth-windows-amd64.exe](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-no-auth-windows-amd64.exe) |

    #### PowerShell Download & Run
    Open PowerShell and run:

    ```powershell
    Invoke-WebRequest -Uri "https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-windows-amd64.exe" -OutFile "dvfs.exe"
    .\dvfs.exe --help
    ```

    > **Windows SmartScreen Note**: If Windows Defender prompts *"Windows protected your PC"*, click **More info** &rarr; **Run anyway**.

=== "macOS"

    ### macOS (Apple Silicon & Intel)

    | Architecture | Binary File | Direct Download Link |
    | :--- | :--- | :--- |
    | **Apple Silicon (M1 / M2 / M3 / M4)** | `dvfs-client-darwin-arm64` | [**Download dvfs-client-darwin-arm64**](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-darwin-arm64){ .md-button .md-button--primary } |
    | **Intel Macs (x86_64)** | `dvfs-client-darwin-amd64` | [**Download dvfs-client-darwin-amd64**](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-darwin-amd64){ .md-button .md-button--primary } |
    | **No-Auth (Apple Silicon)** | `dvfs-client-no-auth-darwin-arm64` | [Download dvfs-client-no-auth-darwin-arm64](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-no-auth-darwin-arm64) |
    | **No-Auth (Intel)** | `dvfs-client-no-auth-darwin-amd64` | [Download dvfs-client-no-auth-darwin-amd64](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-no-auth-darwin-amd64) |

    #### Terminal 1-Line Download & Run
    Open Terminal and run:

    ```bash
    # For Apple Silicon (M1/M2/M3/M4):
    curl -fsSL -o /usr/local/bin/dvfs https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-darwin-arm64
    chmod +x /usr/local/bin/dvfs

    # Verify installation
    dvfs --help
    ```

    > **Gatekeeper Note**: If macOS displays an alert saying the developer cannot be verified, clear the quarantine attribute:
    > ```bash
    > xattr -d com.apple.quarantine /usr/local/bin/dvfs
    > ```

=== "Linux"

    ### Linux (x86_64 & ARM64)

    | Architecture | Binary File | Direct Download Link |
    | :--- | :--- | :--- |
    | **Linux x86_64 (AMD64)** | `dvfs-client-linux-amd64` | [**Download dvfs-client-linux-amd64**](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-linux-amd64){ .md-button .md-button--primary } |
    | **Linux ARM64 (Raspberry Pi 4/5 / Graviton)** | `dvfs-client-linux-arm64` | [**Download dvfs-client-linux-arm64**](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-linux-arm64){ .md-button .md-button--primary } |
    | **No-Auth Linux AMD64** | `dvfs-client-no-auth-linux-amd64` | [Download dvfs-client-no-auth-linux-amd64](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-no-auth-linux-amd64) |
    | **No-Auth Linux ARM64** | `dvfs-client-no-auth-linux-arm64` | [Download dvfs-client-no-auth-linux-arm64](https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-no-auth-linux-arm64) |

    #### Terminal 1-Line Download & Run
    ```bash
    # Download binary directly into /usr/local/bin
    sudo curl -fsSL -o /usr/local/bin/dvfs https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/dvfs-client-linux-amd64
    sudo chmod +x /usr/local/bin/dvfs

    # Verify installation
    dvfs --help
    ```

---

## 2. Authenticated Client vs No-Auth Client

We provide two standalone executable flavors with every release:

| Feature | Authenticated Client (Default) | No-Auth Client (`-no-auth`) |
| :--- | :--- | :--- |
| **Authentication Flow** | Google OAuth 2.0 (RFC 7636 PKCE) | Bypass / Plain Username |
| **Institutional Email Support** | Yes (IITGN or any Google domain) | No |
| **Server-Side Token Verification** | Cryptographically verified ID tokens | Mock / unverified identities |
| **Zero-Trust TLS / mTLS** | Full TLS 1.3 encryption | Full TLS 1.3 encryption |
| **Recommended Use Case** | **Production & all end users** | Offline testing & CI benchmarking |

---

## 3. First Run & Login

### Step 1: Run the Executable
```bash
# Connect to cluster
./dvfs
```

### Step 2: Google Sign-In
When prompted in your terminal:
```text
=========================================================
       Distributed Virtual File System (DVFS)
            Google Authentication Required
=========================================================
Enter your email: your_name@iitgn.ac.in

Please open the following URL in your web browser to sign in:
─────────────────────────────────────────────────────────
https://accounts.google.com/o/oauth2/v2/auth?client_id=...
─────────────────────────────────────────────────────────
```

1. Open the generated URL in your browser.
2. Sign in with your Google account.
3. Your browser will redirect to `http://localhost:38485/logincallback` with **Google Authentication Successful**.
4. Copy the displayed ID token and paste it into the terminal prompt.
5. You are logged in! The session token is cached for subsequent commands.

---

## 4. Verifying Checksums

To verify binary integrity against our official SHA256 checksums:

1. Download checksums:
   ```bash
   curl -fsSL -O https://github.com/DVFS-IIT-Gandhinagar/Distributed-Virtual-File-System/releases/latest/download/SHA256SUMS.txt
   ```
2. Verify:
   * **Linux / macOS**:
     ```bash
     sha256sum -c SHA256SUMS.txt --ignore-missing
     ```
   * **Windows (PowerShell)**:
     ```powershell
     Get-FileHash dvfs-client-windows-amd64.exe -Algorithm SHA256
     ```

---

## 5. Next Steps

* [Client CLI Command Reference](client_cli.md) &mdash; Shell commands (`ls`, `cd`, `put`, `get`, `share`, `trash`).
* [Setup & Deployment Guide](setup.md) &mdash; Detailed cluster node installation guide.
* [Authentication Architecture](features/authentication.md) &mdash; Zero-Trust TLS, Google PKCE, and session tokens.
