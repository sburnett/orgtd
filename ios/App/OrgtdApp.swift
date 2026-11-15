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
            // On launch and every return to the foreground: import
            // share-extension captures, and fetch if the back-off allows
            // (see AppModel.onForeground).
            if phase == .active {
                Task { await model.onForeground() }
            }
        }
    }
}
