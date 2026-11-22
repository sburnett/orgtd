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

/// The two read views, each with the shared settings / sync toolbar,
/// the floating capture button in the lower right, and the tab bar floating over the content. (New entries elsewhere are added from the
/// Outline.)
struct MainTabs: View {
    @Environment(AppModel.self) private var model
    @State private var showSettings = false
    @State private var showCapture = false
    @State private var keyboardShown = false

    var body: some View {
        @Bindable var model = model
        TabView(selection: $model.tab) {
            tab(sync: false) { OutlineView() } // draws Sync itself, after its own buttons
                .tabItem { Label("Outline", systemImage: "list.bullet.indent") }
                .tag(AppTab.outline)
            tab { AgendaView() }
                .tabItem { Label("Agenda", systemImage: "calendar") }
                .tag(AppTab.agenda)
        }
        .sheet(isPresented: $showSettings) {
            NavigationStack { SettingsView() }
        }
        .sheet(isPresented: $showCapture) { CaptureSheet() }
        // The capture button would ride up on the keyboard over the
        // form being typed into, so it steps aside while one is up.
        .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillShowNotification)) { _ in keyboardShown = true }
        .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillHideNotification)) { _ in keyboardShown = false }
        .alert("That didn't work", isPresented: actionErrorAlert) {
            Button("OK") { model.actionError = nil }
        } message: {
            Text(model.actionError ?? "")
        }
        .alert("Some changes couldn't be applied", isPresented: droppedAlert) {
            Button("OK") { Task { await model.dismissDropped() } }
        } message: {
            Text(droppedMessage)
        }
    }

    private var actionErrorAlert: Binding<Bool> {
        Binding(get: { model.actionError != nil }, set: { if !$0 { model.actionError = nil } })
    }

    /// State changes an older version dropped because their entry
    /// changed on the desktop before they were merged.
    private var droppedAlert: Binding<Bool> {
        Binding(get: { !model.status.dropped.isEmpty }, set: { _ in })
    }

    private var droppedMessage: String {
        let lines = model.status.dropped.map { d in
            if d.added == true { return "“\(d.title)”: new entry" }
            if d.deleted == true { return "“\(d.title)”: delete" }
            if d.reordered == true { return "“\(d.title)”: move" }
            if let move = d.move, move != 0 { return "“\(d.title)”: \(move > 0 ? "indent" : "outdent")" }
            return "“\(d.title)”: \(d.from.isEmpty ? "none" : d.from) → \(d.to.isEmpty ? "none" : d.to)"
        }
        return "These entries changed on your computer before the phone's changes to them were merged, so the phone's changes were dropped:\n\n" + lines.joined(separator: "\n")
    }

    /// A tab's navigation stack with the shared toolbar: Settings on the
    /// left and, unless `sync` is false, Sync on the right. A view with
    /// toolbar buttons of its own passes false and puts a `SyncButton`
    /// last in its group, so Sync is always the rightmost button. The
    /// capture button sits in a bottom safe-area inset, so lists scroll
    /// their last rows clear of it.
    private func tab<Content: View>(sync: Bool = true, @ViewBuilder _ content: () -> Content) -> some View {
        NavigationStack {
            content()
                .safeAreaInset(edge: .top) {
                    if !model.status.waiting.isEmpty {
                        WaitingBanner(titles: model.status.waiting)
                    }
                }
                .safeAreaInset(edge: .bottom, alignment: .trailing) {
                    if !keyboardShown {
                        CaptureButton { showCapture = true }
                    }
                }
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        Button { showSettings = true } label: { Image(systemName: "gearshape") }
                            .accessibilityLabel("Settings")
                    }
                    if sync {
                        ToolbarItem(placement: .topBarTrailing) { SyncButton() }
                    }
                }
        }
    }
}

/// Pushes pending captures (and fetches): the rightmost toolbar button
/// on every tab. It also carries the sync status: a spinner while
/// anything is running, a crossed-out Wi-Fi symbol while the server
/// can't be reached, and an exclamation mark after any other failure
/// (Settings has the details). Unless it's busy, a badge on the icon
/// counts the local changes (captures and edits) not pushed yet.
struct SyncButton: View {
    @Environment(AppModel.self) private var model

    private var failed: Bool {
        !model.isOffline && !(model.lastError ?? model.status.lastError).isEmpty
    }

    private var unpushed: Int { model.status.unpushed }

    var body: some View {
        Button { Task { await model.sync() } } label: {
            if model.busy != nil {
                ProgressView()
            } else {
                icon.overlay(alignment: .topTrailing) {
                    if unpushed > 0 { UnpushedBadge(count: unpushed) }
                }
            }
        }
        .disabled(model.busy != nil)
        .accessibilityLabel(accessibilityLabel)
    }

    @ViewBuilder private var icon: some View {
        if model.isOffline {
            Image(systemName: "wifi.slash")
        } else if failed {
            Image(systemName: "exclamationmark.arrow.triangle.2.circlepath")
                .symbolRenderingMode(.multicolor)
        } else {
            Image(systemName: "arrow.triangle.2.circlepath")
        }
    }

    private var accessibilityLabel: String {
        if let busy = model.busy { return busy }
        let state = model.isOffline ? "server unreachable" : failed ? "last attempt failed" : nil
        let changes = unpushed == 0 ? nil : unpushed == 1 ? "1 change to push" : "\(unpushed) changes to push"
        let details = [state, changes].compactMap { $0 }
        return details.isEmpty ? "Sync" : "Sync (\(details.joined(separator: ", ")))"
    }
}

/// The number of unpushed changes, on the Sync button's icon.
private struct UnpushedBadge: View {
    let count: Int

    var body: some View {
        Text(count > 99 ? "99+" : "\(count)")
            .font(.caption2.weight(.bold))
            .monospacedDigit()
            .foregroundStyle(.white)
            .padding(.horizontal, 4)
            .frame(minWidth: 16, minHeight: 16)
            .background(Capsule().fill(.tint))
            .fixedSize()
            .offset(x: 8, y: -8)
            .allowsHitTesting(false)
    }
}


/// Says which of the phone's changes wait for the computer to merge them:
/// their entries changed there too, so the phone shows its own version
/// until the computer merges (and, if they collide, the user resolves
/// them there). Nothing is dropped.
struct WaitingBanner: View {
    let titles: [String]

    var body: some View {
        Label {
            Text("Waiting for your computer to merge the phone's changes to \(titles.map { "“\($0)”" }.joined(separator: ", ")). They changed there too, so open orgtd there; it may ask you to :resolve.")
                .font(.footnote)
        } icon: {
            Image(systemName: "arrow.triangle.merge")
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal)
        .padding(.vertical, 8)
        .background(.yellow.opacity(0.2))
        .accessibilityElement(children: .combine)
    }
}
