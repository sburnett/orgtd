import SwiftUI

extension Color {
    /// A config color: "#rrggbb", or an ANSI 256-color code "0"–"255"
    /// (mapped through the standard xterm palette). nil for "" or junk.
    init?(orgColor s: String) {
        let s = s.trimmingCharacters(in: .whitespaces)
        if s.hasPrefix("#"), s.count == 7, let v = UInt32(s.dropFirst(), radix: 16) {
            self.init(rgb: v)
            return
        }
        if let n = Int(s), (0...255).contains(n) {
            self.init(rgb: Color.xterm256(n))
            return
        }
        return nil
    }

    private init(rgb v: UInt32) {
        self.init(red: Double((v >> 16) & 0xff) / 255, green: Double((v >> 8) & 0xff) / 255, blue: Double(v & 0xff) / 255)
    }

    /// The xterm 256-color palette.
    static func xterm256(_ n: Int) -> UInt32 {
        let base: [UInt32] = [
            0x000000, 0x800000, 0x008000, 0x808000, 0x000080, 0x800080, 0x008080, 0xc0c0c0,
            0x808080, 0xff0000, 0x00ff00, 0xffff00, 0x0000ff, 0xff00ff, 0x00ffff, 0xffffff,
        ]
        if n < 16 { return base[n] }
        if n < 232 {
            let i = n - 16
            let levels: [UInt32] = [0x00, 0x5f, 0x87, 0xaf, 0xd7, 0xff]
            return levels[i / 36] << 16 | levels[(i / 6) % 6] << 8 | levels[i % 6]
        }
        let g = UInt32(8 + (n - 232) * 10)
        return g << 16 | g << 8 | g
    }
}
