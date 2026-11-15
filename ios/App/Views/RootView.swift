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
/// the tab bar floating over the content.
struct MainTabs: View {
    @Environment(AppModel.self) private var model
    @State private var capture: CaptureDraft?
    @State private var showSettings = false

    var body: some View {
        TabView {
            tab { InboxView(capture: { capture = model.newCapture() }, edit: { capture = $0 }) }
                .tabItem { Label("Inbox", systemImage: "tray") }
                .badge(model.status.pending)
            tab(sync: false) { OutlineView() } // draws Sync itself, after its own buttons
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

    /// A tab's navigation stack with the shared toolbar: Settings on the
    /// left and, unless `sync` is false, Sync on the right. A view with
    /// toolbar buttons of its own passes false and puts a `SyncButton`
    /// last in its group, so Sync is always the rightmost button.
    private func tab<Content: View>(sync: Bool = true, @ViewBuilder _ content: () -> Content) -> some View {
        NavigationStack {
            content()
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        Button { showSettings = true } label: { Image(systemName: "gearshape") }
                            .accessibilityLabel("Settings")
                    }
                    if sync {
                        ToolbarItem(placement: .topBarTrailing) { SyncButton() }
                    }
                }
                .navigationDestination(for: Entry.self) { EntryDetailView(entry: $0) }
        }
    }
}

/// Pushes pending captures (and fetches): the rightmost toolbar button
/// on every tab. It also carries the sync status: a spinner while
/// anything is running, a crossed-out Wi-Fi symbol while the server
/// can't be reached, and an exclamation mark after any other failure
/// (Settings has the details).
struct SyncButton: View {
    @Environment(AppModel.self) private var model

    private var failed: Bool {
        !model.isOffline && !(model.lastError ?? model.status.lastError).isEmpty
    }

    var body: some View {
        Button { Task { await model.sync() } } label: {
            if model.busy != nil {
                ProgressView()
            } else if model.isOffline {
                Image(systemName: "wifi.slash")
            } else if failed {
                Image(systemName: "exclamationmark.arrow.triangle.2.circlepath")
                    .symbolRenderingMode(.multicolor)
            } else {
                Image(systemName: "arrow.triangle.2.circlepath")
            }
        }
        .disabled(model.busy != nil)
        .accessibilityLabel(model.busy ?? (model.isOffline ? "Sync (server unreachable)" : failed ? "Sync (last attempt failed)" : "Sync"))
    }
}

// A sheet's item: `id` is the capture's queue ID, nil for a new one.
extension CaptureDraft: Identifiable {}

