import SwiftUI

/// The read-only outline: each file's headlines as disclosure groups, a
/// separate Reference section, and search. Picking a search result opens
/// the outline at that entry.
struct OutlineView: View {
    @Environment(AppModel.self) private var model
    @State private var query = ""
    @State private var results: [Entry] = []
    @State private var expanded: Set<String> = []
    @State private var target: String?

    var body: some View {
        @Bindable var model = model
        ScrollViewReader { proxy in
            List {
                if !query.isEmpty {
                    if results.isEmpty {
                        ContentUnavailableView.search(text: query)
                    }
                    ForEach(results) { entry in
                        Button {
                            reveal(entry)
                        } label: {
                            EntryRow(entry: entry, showPlace: true)
                        }
                        .buttonStyle(.plain)
                    }
                } else if let outline = model.outline {
                    ForEach(outline.files) { file in
                        Section(file.file) { nodes(file.entries) }
                    }
                    if !outline.reference.isEmpty {
                        ForEach(outline.reference) { file in
                            Section("Reference · \(file.file.replacingOccurrences(of: "reference/", with: ""))") {
                                nodes(file.entries)
                            }
                        }
                    }
                }
            }
            .onChange(of: target) { _, id in
                guard let id else { return }
                withAnimation { proxy.scrollTo(id, anchor: .center) }
                target = nil
            }
        }
        .navigationTitle("Outline")
        .searchable(text: $query, prompt: "Search titles, tags, notes")
        .task(id: query) {
            results = query.isEmpty ? [] : await model.search(query)
        }
        .toolbar {
            ToolbarItem(placement: .secondaryAction) {
                Toggle("Show closed items", isOn: $model.showClosed)
            }
            ToolbarItem(placement: .secondaryAction) {
                Button("Collapse all") { expanded = [] }
            }
        }
        .refreshable { await model.refresh() }
    }

    private func nodes(_ entries: [Entry]) -> some View {
        ForEach(entries) { entry in
            OutlineNode(entry: entry, expanded: $expanded)
        }
    }

    /// Leaves search, expands the entry's ancestors, and scrolls to it.
    private func reveal(_ entry: Entry) {
        expanded.formUnion(entry.ancestorIDs)
        query = ""
        Task { @MainActor in
            // Let the outline rows exist before scrolling to one.
            try? await Task.sleep(for: .milliseconds(150))
            target = entry.id
        }
    }
}

private struct OutlineNode: View {
    let entry: Entry
    @Binding var expanded: Set<String>

    var body: some View {
        if let children = entry.children, !children.isEmpty {
            DisclosureGroup(isExpanded: binding) {
                ForEach(children) { child in
                    OutlineNode(entry: child, expanded: $expanded)
                }
            } label: {
                link
            }
            .id(entry.id)
        } else {
            link.id(entry.id)
        }
    }

    private var link: some View {
        NavigationLink(value: entry) { EntryRow(entry: entry) }
    }

    private var binding: Binding<Bool> {
        Binding(
            get: { expanded.contains(entry.id) },
            set: { open in
                if open { expanded.insert(entry.id) } else { expanded.remove(entry.id) }
            }
        )
    }
}
