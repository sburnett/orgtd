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
            tab { InboxView(capture: { capture = model.newCapture() }, edit: { capture = $0 }) }
                .tabItem { Label("Inbox", systemImage: "tray") }
                .badge(model.status.pending)
            tab { OutlineView() }
                .tabItem { Label("Outline", systemImage: "list.bullet.indent") }
            tab { AgendaView() }
                .tabItem { Label("Agenda", systemImage: "calendar") }
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
                    ToolbarItem(placement: .topBarTrailing) {
                        Button { Task { await model.sync() } } label: {
                            Image(systemName: "arrow.triangle.2.circlepath")
                        }
                        .disabled(model.busy != nil)
                        .accessibilityLabel("Sync")
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
/// how many captures are waiting. Every tab has its own copy, so the
/// relative times are computed from a shared periodic clock (not from
/// whenever that tab last happened to redraw): all copies agree, and
/// they stay current while the app sits open.
struct StatusLine: View {
    @Environment(AppModel.self) private var model
    var openSettings: () -> Void

    /// How often the relative times are recomputed. They're shown to the
    /// minute ("just now" under one), so this only bounds how late a
    /// change of minute shows up.
    private static let tick: TimeInterval = 10

    var body: some View {
        TimelineView(.periodic(from: .now, by: Self.tick)) { context in
            Button(action: openSettings) {
                HStack(spacing: 6) {
                    if let busy = model.busy {
                        ProgressView().controlSize(.small)
                        Text(busy)
                    } else if model.isOffline {
                        Image(systemName: "wifi.slash")
                        Text("Can't reach server · " + summary(now: context.date)).lineLimit(1)
                    } else if let error = model.lastError ?? nonEmpty(model.status.lastError) {
                        Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        Text(error).lineLimit(1)
                    } else {
                        Text(summary(now: context.date))
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
    }

    private func summary(now: Date) -> String {
        var parts: [String] = []
        if let fetched = Self.relative(model.status.lastFetch, now: now) { parts.append("Fetched \(fetched)") }
        if let pushed = Self.relative(model.status.lastPush, now: now) { parts.append("pushed \(pushed)") }
        let s = model.status
        if s.pending > 0 {
            parts.append(s.unpushed > 0 ? "\(s.pending) pending (\(s.unpushed) not pushed)" : "\(s.pending) pending merge")
        }
        return parts.isEmpty ? "Not synced yet" : parts.joined(separator: " · ")
    }

    private func nonEmpty(_ s: String) -> String? { s.isEmpty ? nil : s }

    /// "just now" under a minute, then "5 min. ago", "2 hr. ago", ...
    static func relative(_ iso: String, now: Date) -> String? {
        guard let d = ISO8601DateFormatter().date(from: iso) else { return nil }
        if now.timeIntervalSince(d) < 60 { return "just now" }
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        return f.localizedString(for: d, relativeTo: now)
    }
}
