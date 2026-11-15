import SwiftUI

struct RootView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        if model.isSetUp {
            MainTabs()
        } else {
            NavigationStack { OnboardingView() }
        }
    }
}

/// The three read views, each with the shared capture / sync toolbar and
/// the status line along the bottom.
struct MainTabs: View {
    @Environment(AppModel.self) private var model
    @State private var capture: CaptureDraft?
    @State private var showSettings = false

    var body: some View {
        TabView {
            tab { AgendaView() }
                .tabItem { Label("Agenda", systemImage: "calendar") }
            tab { InboxView(edit: { capture = $0 }) }
                .tabItem { Label("Inbox", systemImage: "tray") }
                .badge(model.status.pending)
            tab { OutlineView() }
                .tabItem { Label("Outline", systemImage: "list.bullet.indent") }
        }
        .sheet(item: $capture) { draft in
            CaptureSheet(draft: draft)
        }
        .sheet(isPresented: $showSettings) {
            NavigationStack { SettingsView() }
        }
    }

    private func tab<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        NavigationStack {
            content()
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        Button { showSettings = true } label: { Image(systemName: "gearshape") }
                            .accessibilityLabel("Settings")
                    }
                    ToolbarItemGroup(placement: .topBarTrailing) {
                        Button { Task { await model.sync() } } label: {
                            Image(systemName: "arrow.triangle.2.circlepath")
                        }
                        .disabled(model.busy != nil)
                        .accessibilityLabel("Sync")
                        Button { capture = model.newCapture() } label: { Image(systemName: "plus") }
                            .accessibilityLabel("Capture")
                    }
                }
                .navigationDestination(for: Entry.self) { EntryDetailView(entry: $0) }
                .safeAreaInset(edge: .bottom) {
                    StatusLine(openSettings: { showSettings = true })
                }
        }
    }
}

// A sheet's item: `id` is the capture's queue ID, nil for a new one.
extension CaptureDraft: Identifiable {}

/// One line: what's running, or the last error, or sync freshness and
/// how many captures are waiting.
struct StatusLine: View {
    @Environment(AppModel.self) private var model
    var openSettings: () -> Void

    var body: some View {
        Button(action: openSettings) {
            HStack(spacing: 6) {
                if let busy = model.busy {
                    ProgressView().controlSize(.small)
                    Text(busy)
                } else if let error = model.lastError ?? nonEmpty(model.status.lastError) {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(error).lineLimit(1)
                } else {
                    Text(summary)
                }
                Spacer(minLength: 0)
            }
            .font(.footnote)
            .foregroundStyle(.secondary)
            .padding(.horizontal)
            .padding(.vertical, 6)
            .background(.bar)
        }
        .buttonStyle(.plain)
    }

    private var summary: String {
        var parts: [String] = []
        if let fetched = relative(model.status.lastFetch) { parts.append("Fetched \(fetched)") }
        if let pushed = relative(model.status.lastPush) { parts.append("pushed \(pushed)") }
        let s = model.status
        if s.pending > 0 {
            parts.append(s.unpushed > 0 ? "\(s.pending) pending (\(s.unpushed) not pushed)" : "\(s.pending) pending merge")
        }
        return parts.isEmpty ? "Not synced yet" : parts.joined(separator: " · ")
    }

    private func nonEmpty(_ s: String) -> String? { s.isEmpty ? nil : s }

    private func relative(_ iso: String) -> String? {
        guard let d = ISO8601DateFormatter().date(from: iso) else { return nil }
        return RelativeDateTimeFormatter().localizedString(for: d, relativeTo: Date())
    }
}
