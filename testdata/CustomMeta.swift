import Foundation

private struct MetaKey: CodingKey {
    let stringValue: String
    var intValue: Int? { nil }
    init?(stringValue: String) { self.stringValue = stringValue }
    init?(intValue: Int) { return nil }
}
public struct testCustomMeta: Codable {
    public var kind: String
    public var identity: testMetaIdentity
    public var extra: [String: JSONValue]
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: MetaKey.self)
        kind = try c.decode(String.self, forKey: MetaKey(stringValue: "kind")!)
        identity = try c.decode(testMetaIdentity.self, forKey: MetaKey(stringValue: "identity")!)
        extra = [:]
        for key in c.allKeys where key.stringValue != "identity" && key.stringValue != "kind" { extra[key.stringValue] = try c.decode(JSONValue.self, forKey: key) }
    }
    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: MetaKey.self)
        for (key, value) in extra where key != "identity" && key != "kind" { try c.encode(value, forKey: MetaKey(stringValue: key)!) }
        try c.encode(kind, forKey: MetaKey(stringValue: "kind")!)
        try c.encode(identity, forKey: MetaKey(stringValue: "identity")!)
    }
}
