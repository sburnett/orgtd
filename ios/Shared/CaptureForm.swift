import SwiftUI

/// The share extension's capture form (the app's inline forms have the
/// same fields; see InlineForm): title (required), body, tags with autocomplete, and
/// SCHEDULED/DEADLINE. `parseDate`, when given (the app has the Go core;
/// the extension doesn't), adds a free-text date field that accepts
/// exactly what the desktop's "gd" does ("3d", "2w", "next tue").
struct CaptureForm: View {
    @Binding var draft: CaptureDraft
    var keywords: [KeywordOption]
    var knownTags: [String]
    var parseDate: ((String) async throws -> String)?
    @FocusState private var titleFocused: Bool

    var body: some View {
        Form {
            Section {
                if !keywords.isEmpty {
                    KeywordPicker(keyword: $draft.keyword, options: keywords)
                }
                TextField("Title", text: $draft.title, axis: .vertical)
                    .focused($titleFocused)
                    .submitLabel(.next)
                TextField("Notes", text: $draft.body, axis: .vertical)
                    .lineLimit(3...12)
            }
            Section("Tags") {
                TagField(tags: $draft.tags, knownTags: knownTags)
            }
            Section("Dates") {
                DateField(label: "Scheduled", value: $draft.scheduled, parseDate: parseDate)
                DateField(label: "Deadline", value: $draft.deadline, parseDate: parseDate)
            }
        }
        .onAppear { if draft.trimmedTitle.isEmpty { titleFocused = true } }
    }
}

/// The keyword as a row of chips: None, then each configured keyword in
/// its config color.
struct KeywordPicker: View {
    @Binding var keyword: String
    var options: [KeywordOption]

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                chip("None", value: "", color: .secondary)
                ForEach(options) { k in
                    chip(k.name, value: k.name, color: Color(orgColor: k.color) ?? .accentColor)
                }
            }
            .padding(.vertical, 2)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Keyword")
    }

    private func chip(_ label: String, value: String, color: Color) -> some View {
        let selected = keyword == value
        return Button {
            keyword = value
        } label: {
            Text(label)
                .font(.caption.monospaced().weight(selected ? .bold : .regular))
                .foregroundStyle(selected ? color : .secondary)
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .background(Capsule().fill(selected ? color.opacity(0.18) : Color.secondary.opacity(0.08)))
                .overlay(Capsule().strokeBorder(selected ? color : .clear, lineWidth: 1))
        }
        .buttonStyle(.borderless)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// Tags as removable chips plus a text field that suggests known tags.
struct TagField: View {
    @Binding var tags: [String]
    var knownTags: [String]
    @State private var text = ""

    private var suggestions: [String] {
        let q = text.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return [] }
        return knownTags.filter { $0.lowercased().hasPrefix(q) && !tags.contains($0) }.prefix(8).map { $0 }
    }

    var body: some View {
        if !tags.isEmpty {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack {
                    ForEach(tags, id: \.self) { tag in
                        Button {
                            tags.removeAll { $0 == tag }
                        } label: {
                            Label(tag, systemImage: "xmark.circle.fill")
                                .labelStyle(TagChipLabelStyle())
                        }
                        .buttonStyle(.borderless)
                        .accessibilityLabel("Remove tag \(tag)")
                    }
                }
            }
        }
        TextField("Add tag", text: $text)
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .submitLabel(.done)
            .onSubmit { add(text) }
        ForEach(suggestions, id: \.self) { s in
            Button {
                add(s)
            } label: {
                Label(s, systemImage: "tag")
            }
            .buttonStyle(.borderless) // in a list row, a plain button would take over the row
        }
    }

    private func add(_ raw: String) {
        let tag = raw.trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: ":"))
        text = ""
        guard !tag.isEmpty, !tags.contains(tag) else { return }
        tags.append(tag)
    }
}

private struct TagChipLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 4) {
            configuration.title
            configuration.icon.imageScale(.small)
        }
        .font(.callout)
        .padding(.horizontal, 10)
        .padding(.vertical, 4)
        .background(Capsule().fill(Color.accentColor.opacity(0.15)))
    }
}

/// An optional date: off, or a date picker — plus, with `parseDate`, a
/// field for typing it.
struct DateField: View {
    let label: String
    @Binding var value: String
    var parseDate: ((String) async throws -> String)?
    @State private var typed = ""
    @State private var error: String?

    private var isOn: Binding<Bool> {
        Binding(
            get: { !value.isEmpty },
            set: { on in value = on ? OrgDate.string(from: Date()) : "" }
        )
    }

    private var date: Binding<Date> {
        Binding(
            get: { OrgDate.date(from: value) ?? Date() },
            set: { value = OrgDate.string(from: $0, withTime: value.count > 10) }
        )
    }

    var body: some View {
        Toggle(label, isOn: isOn)
        if !value.isEmpty {
            DatePicker(label, selection: date, displayedComponents: .date)
                .datePickerStyle(.compact)
                .labelsHidden()
        }
        if let parseDate {
            HStack {
                TextField("Type a \(label.lowercased()) (3d, next tue…)", text: $typed)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .submitLabel(.done)
                    .onSubmit { Task { await parse(parseDate) } }
                if !typed.isEmpty {
                    Button("Set") { Task { await parse(parseDate) } }
                        .buttonStyle(.borderless)
                }
            }
            if let error {
                Text(error).font(.footnote).foregroundStyle(.red)
            }
        }
    }

    @MainActor
    private func parse(_ parseDate: (String) async throws -> String) async {
        let input = typed.trimmingCharacters(in: .whitespaces)
        guard !input.isEmpty else { return }
        do {
            value = try await parseDate(input)
            typed = ""
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }
}
