import SwiftUI
import UIKit
import UniformTypeIdentifiers

/// The share-sheet entry point. It pre-fills a capture from what was
/// shared (a URL goes in the body, the page title or text in the title)
/// and, on Save, writes it to the App Group's incoming directory — its
/// only write. It doesn't link the Go core or touch git; the app imports
/// the capture the next time it comes to the foreground.
final class ShareViewController: UIViewController {
    override func viewDidLoad() {
        super.viewDidLoad()
        Task { @MainActor [weak self] in
            let draft = await Self.draft(from: self?.extensionContext?.inputItems as? [NSExtensionItem] ?? [])
            self?.show(draft)
        }
    }

    private func show(_ draft: CaptureDraft) {
        let root = ShareCaptureView(
            draft: draft,
            knownTags: TagsCache.load(),
            done: { [weak self] in self?.extensionContext?.completeRequest(returningItems: nil) },
            cancel: { [weak self] in
                self?.extensionContext?.cancelRequest(withError: NSError(domain: NSCocoaErrorDomain, code: NSUserCancelledError))
            }
        )
        let host = UIHostingController(rootView: root)
        addChild(host)
        host.view.frame = view.bounds
        host.view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        view.addSubview(host.view)
        host.didMove(toParent: self)
    }

    private static func draft(from items: [NSExtensionItem]) async -> CaptureDraft {
        var draft = CaptureDraft()
        var url: URL?
        var text: String?
        for item in items {
            if draft.title.isEmpty {
                let title = item.attributedTitle?.string ?? item.attributedContentText?.string ?? ""
                draft.title = title.trimmingCharacters(in: .whitespacesAndNewlines)
            }
            for provider in item.attachments ?? [] {
                if url == nil, provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
                    url = try? await provider.loadItem(forTypeIdentifier: UTType.url.identifier) as? URL
                } else if text == nil, provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
                    text = try? await provider.loadItem(forTypeIdentifier: UTType.plainText.identifier) as? String
                }
            }
        }
        if let url {
            draft.body = url.absoluteString
            if let text, draft.title.isEmpty { draft.title = text }
        } else if let text {
            // Plain text: a short one is the title; a long one the notes.
            if draft.title.isEmpty, !text.contains("\n"), text.count <= 120 {
                draft.title = text
            } else {
                draft.body = text
            }
        }
        if draft.title == draft.body { draft.title = "" }
        return draft
    }
}

struct ShareCaptureView: View {
    @State var draft: CaptureDraft
    var knownTags: [String]
    var done: () -> Void
    var cancel: () -> Void
    @State private var error: String?

    var body: some View {
        NavigationStack {
            CaptureForm(draft: $draft, knownTags: knownTags, parseDate: nil)
                .safeAreaInset(edge: .bottom) {
                    if let error {
                        Text(error).font(.footnote).foregroundStyle(.red).padding()
                    } else if !AppGroup.isShared {
                        Text("No App Group: orgtd can't receive shared items in this build.")
                            .font(.footnote).foregroundStyle(.orange).padding()
                    }
                }
                .navigationTitle("Capture to orgtd")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel", action: cancel) }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Save") { save() }.disabled(draft.trimmedTitle.isEmpty)
                    }
                }
        }
    }

    private func save() {
        do {
            draft.createdAt = Date()
            try draft.writeIncoming()
            done()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
