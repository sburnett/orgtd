import SwiftUI

/// The inbox file's open top-level items in file order, with a large
/// Capture button at the bottom. Captures that haven't been merged yet
/// are badged, and — since they still live only in the queue — can be
/// edited or deleted by swiping.
struct InboxView: View {
    @Environment(AppModel.self) private var model
    var capture: () -> Void
    var edit: (CaptureDraft) -> Void

    var body: some View {
        List {
            if let inbox = model.inbox {
                if inbox.entries.isEmpty {
                    ContentUnavailableView("Inbox zero", systemImage: "tray")
                }
                ForEach(inbox.entries) { entry in
                    NavigationLink(value: entry) {
                        CompactEntryRow(entry: entry)
                    }
                    .compactEntryRowInsets()
                    .stateContextMenu(entry)
                    .swipeActions(edge: .trailing) {
                        if let id = entry.captureId {
                            Button(role: .destructive) {
                                Task { await model.deleteCapture(id: id) }
                            } label: { Label("Delete", systemImage: "trash") }
                            Button {
                                Task {
                                    if let draft = await model.pendingCapture(id: id) { edit(draft) }
                                }
                            } label: { Label("Edit", systemImage: "pencil") }
                            .tint(.blue)
                        }
                    }
                }
            }
        }
        .compactEntryList()
        .navigationTitle("Inbox")
        .safeAreaInset(edge: .bottom) {
            Button(action: capture) {
                Label("Capture", systemImage: "plus")
                    .font(.headline)
                    .frame(maxWidth: .infinity, minHeight: 34)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .padding(.horizontal)
            .padding(.vertical, 8)
        }
        .refreshable { await model.refresh() }
    }
}
