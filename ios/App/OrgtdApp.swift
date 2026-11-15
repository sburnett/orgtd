import SwiftUI

@main
struct OrgtdApp: App {
    @State private var model = AppModel()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(model)
                .task { await model.reloadAll() }
        }
        .onChange(of: scenePhase) { _, phase in
            // Fetch on launch and on every return to the foreground; it's
            // read-only and also imports share-extension captures.
            if phase == .active {
                Task { await model.refresh() }
            }
        }
    }
}
