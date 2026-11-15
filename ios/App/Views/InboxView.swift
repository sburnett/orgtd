import SwiftUI

/// The inbox file's open top-level items in file order. Captures that
/// haven't been merged yet are badged, and — since they still live only
/// in the queue — can be edited or deleted by swiping.
struct InboxView: View {
    @Environment(AppModel.self) private var model
    var edit: (CaptureDraft) -> Void

    var body: some View {
        List {
            if let inbox = model.inbox {
                if inbox.entries.isEmpty {
                    ContentUnavailableView("Inbox zero", systemImage: "tray")
                }
                ForEach(inbox.entries) { entry in
                    NavigationLink(value: entry) {
                        HStack {
                            EntryRow(entry: entry)
                            if entry.isPending {
                                Spacer()
                                Image(systemName: "clock.badge")
                                    .foregroundStyle(.orange)
                                    .accessibilityLabel("Pending")
                            }
                        }
                    }
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
        .navigationTitle(model.inbox?.file ?? "Inbox")
        .refreshable { await model.refresh() }
    }
}
