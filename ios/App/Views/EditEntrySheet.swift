import SwiftUI

/// Edits an existing entry with the capture form: keyword, title, notes,
/// tags and dates. Only the fields that change are written back (see
/// mobilecore.EditEntry), so an untouched date keeps its repeater. A
/// capture that hasn't been merged yet is edited as a capture instead.
struct EditEntrySheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let entry: Entry
    @State private var draft: CaptureDraft
    @State private var loading: Bool
    @State private var saving = false
    @State private var error: String?

    init(entry: Entry) {
        self.entry = entry
        _loading = State(initialValue: entry.captureId != nil)
        _draft = State(initialValue: CaptureDraft(
            keyword: entry.keyword,
            title: entry.title,
            body: entry.body,
            tags: entry.tags,
            scheduled: entry.scheduledInput ?? "",
            deadline: entry.deadlineInput ?? ""
        ))
    }

    var body: some View {
        NavigationStack {
            Group {
                if loading {
                    ProgressView()
                } else {
                    CaptureForm(
                        draft: $draft,
                        keywords: entry.isReference ? [] : model.allKeywords,
                        knownTags: model.tags,
                        parseDate: { try await model.parseDate($0) }
                    )
                }
            }
            .safeAreaInset(edge: .bottom) {
                if let error {
                    Text(error).font(.footnote).foregroundStyle(.red).padding()
                }
            }
            .navigationTitle("Edit")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { Task { await save() } }
                        .disabled(draft.trimmedTitle.isEmpty || saving || loading)
                }
            }
        }
        .interactiveDismissDisabled()
        .task {
            // A pending capture is still only in the queue: edit it there.
            guard let id = entry.captureId else { return }
            if let capture = await model.pendingCapture(id: id) { draft = capture }
            loading = false
        }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        let ok = entry.captureId != nil ? await model.save(draft) : await model.edit(entry, to: draft)
        if ok {
            dismiss()
        } else {
            error = model.lastError
        }
    }
}
