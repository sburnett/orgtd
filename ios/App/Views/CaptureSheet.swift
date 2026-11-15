import SwiftUI

/// The floating capture button, in the lower right corner of every tab
/// (above the tab bar). It opens `CaptureSheet`.
struct CaptureButton: View {
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: "plus")
                .font(.title2.weight(.semibold))
                .foregroundStyle(.white)
                .frame(width: 56, height: 56)
                .background(Circle().fill(Color.accentColor))
                .shadow(color: .black.opacity(0.25), radius: 6, y: 3)
        }
        .buttonStyle(.plain)
        .padding(.trailing, 16)
        .padding(.bottom, 12)
        .accessibilityLabel("Capture")
    }
}

/// A new entry at the end of the inbox file, from anywhere in the app:
/// the capture form's fields, saved as the Outline's new entries are
/// (committed locally at once, with a CREATED stamp; Sync pushes it).
struct CaptureSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var draft = CaptureDraft()
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
                .navigationTitle("Capture")
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
        .onAppear { draft = model.newCapture() }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        draft.createdAt = Date()
        // Appending at the end of a file's top level renumbers nothing,
        // so the Outline has no IDs to carry over.
        if await model.addEntry(draft, file: model.status.inboxFile, at: .last(parent: nil), placed: { _ in }) {
            dismiss()
        } else {
            error = model.lastError
        }
    }
}
