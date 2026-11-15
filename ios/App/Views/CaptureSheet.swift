import SwiftUI

/// New capture, or editing one that's still pending. Saving commits it
/// locally right away (no network); Sync pushes it.
struct CaptureSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State var draft: CaptureDraft
    @State private var saving = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            CaptureForm(draft: $draft, keywords: model.captureKeywords, knownTags: model.tags, parseDate: { try await model.parseDate($0) })
                .safeAreaInset(edge: .bottom) {
                    if let error {
                        Text(error).font(.footnote).foregroundStyle(.red).padding()
                    }
                }
                .navigationTitle(draft.id == nil ? "Capture" : "Edit capture")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { dismiss() }
                    }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Save") { Task { await save() } }
                            .disabled(draft.trimmedTitle.isEmpty || saving)
                    }
                }
        }
        .interactiveDismissDisabled(!draft.trimmedTitle.isEmpty)
    }

    private func save() async {
        saving = true
        defer { saving = false }
        if await model.save(draft) {
            dismiss()
        } else {
            error = model.lastError
        }
    }
}
