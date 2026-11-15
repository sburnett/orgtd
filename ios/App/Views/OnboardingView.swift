import SwiftUI

/// First run: make a key, authorize it on the server, enter the
/// repository and author, confirm the server's host key, clone.
struct OnboardingView: View {
    @Environment(AppModel.self) private var model
    @State private var keyError: String?

    var body: some View {
        @Bindable var model = model
        Form {
            Section {
                Text("orgtd keeps its own clone of your org repository, fetched over SSH from any git server you can reach. Everything it writes goes on a separate “mobile” branch you merge on your computer.")
                    .font(.callout)
            }
            Section("1. SSH key") {
                if let key = model.publicKey {
                    PublicKeyView(key: key)
                    Text("On the server, add the restricted line to `~/.ssh/authorized_keys` of the account that owns the repository. It lets this key run git and nothing else.")
                        .font(.footnote)
                } else {
                    Button("Generate SSH key") {
                        do { try model.generateKey() } catch { keyError = error.localizedDescription }
                    }
                    if let keyError { Text(keyError).foregroundStyle(.red) }
                }
            }
            Section {
                TextField("you@server:org.git", text: $model.repoURL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
                TextField("Your name (for commits)", text: $model.authorName)
                    .textContentType(.name)
                TextField("Your email (for commits)", text: $model.authorEmail)
                    .textInputAutocapitalization(.never)
                    .keyboardType(.emailAddress)
                    .textContentType(.emailAddress)
            } header: {
                Text("2. Repository")
            } footer: {
                Text("user@host:path/to/repo.git, or ssh://user@host:port/path/to/repo.git")
            }
            Section("3. Server") {
                if let hk = model.serverHostKey {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("\(hk.host) presented this host key:").font(.footnote)
                        Text(hk.fingerprint)
                            .font(.callout.monospaced())
                            .textSelection(.enabled)
                        Text("Check it matches before trusting it. On the server, run\n`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`\n(or the key type shown above). If it doesn't match, don't continue.")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                    Button {
                        Task { await model.clone() }
                    } label: {
                        busyLabel("Trust this server and clone")
                    }
                    .disabled(model.busy != nil)
                    Button("Cancel", role: .cancel) { model.serverHostKey = nil }
                } else {
                    Button {
                        Task { await model.checkServer() }
                    } label: {
                        busyLabel("Check server")
                    }
                    .disabled(model.publicKey == nil || model.trimmedRepoURL.isEmpty || model.busy != nil)
                }
                if let error = model.lastError {
                    Text(error).font(.footnote).foregroundStyle(.red)
                }
            }
            if !AppGroup.isShared {
                Section {
                    Label("The App Group isn't available in this build, so the share extension can't hand captures to the app. See ios/README.md.", systemImage: "exclamationmark.triangle")
                        .font(.footnote)
                }
            }
        }
        .navigationTitle("Set up orgtd")
        .onChange(of: model.repoURL) { model.serverHostKey = nil }
    }

    private func busyLabel(_ title: String) -> some View {
        HStack {
            Text(title)
            if model.busy != nil {
                Spacer()
                ProgressView()
            }
        }
    }
}

/// The phone's public key, plain and as a restricted authorized_keys line.
struct PublicKeyView: View {
    let key: String
    @State private var copied: String?

    /// Limits the key to git: git-shell runs only git-upload-pack,
    /// git-receive-pack and git-upload-archive, and `restrict` turns off
    /// port, agent and X11 forwarding and PTY allocation.
    static func authorizedKeysLine(_ key: String) -> String {
        #"command="git-shell -c \"$SSH_ORIGINAL_COMMAND\"",restrict "# + key
    }

    var body: some View {
        Text(key)
            .font(.caption.monospaced())
            .textSelection(.enabled)
            .lineLimit(4)
        Button(copied == "line" ? "Copied" : "Copy authorized_keys line (restricted)") {
            UIPasteboard.general.string = PublicKeyView.authorizedKeysLine(key)
            copied = "line"
        }
        Button(copied == "key" ? "Copied" : "Copy public key only") {
            UIPasteboard.general.string = key
            copied = "key"
        }
        ShareLink(item: PublicKeyView.authorizedKeysLine(key)) {
            Label("Share authorized_keys line", systemImage: "square.and.arrow.up")
        }
    }
}
