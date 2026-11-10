// Exercises the generated Swift JSON-RPC request against the fixture server.
import Foundation
@_exported import FoundationNetworking

@main struct MCPClient {
    static func main() async throws {
        let client = ApiClient(baseURL: CommandLine.arguments[1])
        let req = try JSONDecoder().decode(JSONRPCRequest.self, from: Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[2])))
        let response = try await client.mcp(req: req, headers: ["Mcp-Protocol-Version": "2026-07-28", "Mcp-Method": "server/discover"])
        guard case .value(.object(let value)) = response.result, case .string("ready") = value["instructions"] else { fatalError("unexpected discovery") }
    }
}
