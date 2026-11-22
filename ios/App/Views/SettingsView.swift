import SwiftUI

/// Sync status (with the full text of the last error), the SSH key and the server's host key,
/// the repository, and the author used for capture commits.
struct SettingsView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var confirmRemove = false

    var body: some View {
        @Bindable var model = model
        Form {
            Section("Status") {
                LabeledContent("Last fetch", value: format(model.status.lastFetch))
                LabeledContent("Last push", value: format(model.status.lastPush))
                LabeledContent("Pending captures", value: "\(model.status.pending)")
                LabeledContent("Not yet pushed", value: "\(model.status.unpushed)")
                LabeledContent("Sent, not merged yet", value: "\(model.status.sent)")
                if let error = model.lastError ?? (model.status.lastError.isEmpty ? nil : model.status.lastError) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Last error").font(.caption).foregroundStyle(.secondary)
                        Text(error).font(.callout).textSelection(.enabled)
                        Text(hint(for: error)).font(.footnote).foregroundStyle(.secondary)
                    }
                }
                ForEach(model.status.configErrors, id: \.self) { warning in
                    Label(warning, systemImage: "exclamationmark.triangle").font(.footnote)
                }
                Button("Sync now") { Task { await model.sync() } }
                    .disabled(model.busy != nil)
            }
            Section("Repository") {
                LabeledContent("Remote", value: model.status.repoURL)
                if !model.status.hostKey.fingerprint.isEmpty {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Trusted host key (\(model.status.hostKey.host))").font(.caption).foregroundStyle(.secondary)
                        Text(model.status.hostKey.fingerprint).font(.caption.monospaced()).textSelection(.enabled)
                    }
                }
                LabeledContent("Merges into", value: model.status.baseBranch)
                LabeledContent("Inbox file", value: model.status.inboxFile)
                Text("Captures are pushed to the **mobile** branch. Merge it into \(model.status.baseBranch) on your computer (a normal merge or fast-forward; not squash).")
                    .font(.footnote)
                Button("Remove clone…", role: .destructive) { confirmRemove = true }
            }
            Section("Commit author") {
                TextField("Name", text: $model.authorName)
                TextField("Email", text: $model.authorEmail)
                    .textInputAutocapitalization(.never)
                    .keyboardType(.emailAddress)
            }
            Section("SSH key") {
                if let key = model.publicKey {
                    PublicKeyView(key: key)
                }
            }
            Section("Share extension") {
                Label(AppGroup.isShared ? "Shared container available" : "No App Group: sharing to orgtd won't reach the app",
                      systemImage: AppGroup.isShared ? "checkmark.circle" : "xmark.circle")
                    .font(.footnote)
            }
        }
        .navigationTitle("Settings")
        .toolbar {
            ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
        }
        .confirmationDialog("Remove the clone?", isPresented: $confirmRemove) {
            Button("Remove clone", role: .destructive) {
                Task {
                    await model.removeClone()
                    dismiss()
                }
            }
        } message: {
            Text("Pending captures are kept and will be added again once you clone a repository.")
        }
    }

    private func format(_ iso: String) -> String {
        guard let d = ISO8601DateFormatter().date(from: iso) else { return "Never" }
        return d.formatted(date: .abbreviated, time: .shortened)
    }

    private func hint(for error: String) -> String {
        let e = error.lowercased()
        if e.contains("changed by someone else") {
            return "Lease failure: the mobile branch on the server isn't what this phone last pushed. Merge or delete it on your computer, then sync."
        }
        if e.contains("can't reach the git server") {
            return "The server didn't answer (no network, or you're away from the network it's on). Nothing was lost: captures stay on the phone and are pushed on the next sync that gets through. The app tries again on its own when you reopen it, less often while the server stays unreachable."
        }
        if e.contains("host key doesn't match") {
            return "Something answered at the server's address but identified itself with a different host key than the one trusted at setup, so the app refused to talk to it. If you're on another network (a café, a friend's Wi-Fi) that happens to use the same local address, that's expected and harmless: a hostname that only resolves on your network, or a VPN like Tailscale, avoids it. If you reinstalled the server or changed its keys, remove the clone and set it up again, checking the new fingerprint. Otherwise, don't sync: someone may be impersonating the server."
        }
        if e.contains("unable to authenticate") || e.contains("permission") || e.contains("handshake") {
            return "Authentication failed: check that this phone's key is in the server account's ~/.ssh/authorized_keys, and that the account can write to the repository."
        }
        if e.contains("no such host") || e.contains("network") || e.contains("timeout") || e.contains("connection") {
            return "Network problem: captures are safe on the phone; sync again when you're online."
        }
        return ""
    }
}
