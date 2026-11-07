// Exercises typed URL queries independently of JSON bodies and SSE responses.
import Foundation
@_exported import FoundationNetworking

// Query fixtures are bounded single-event responses; lifecycle tests use SSETransport.swift.
extension URLSession {
    struct AsyncBytes: AsyncSequence {
        typealias Element = UInt8
        let data: Data
        let task: URLSessionDataTask
        struct Iterator: AsyncIteratorProtocol {
            var iterator: Data.Iterator
            mutating func next() async throws -> UInt8? { iterator.next() }
        }
        func makeAsyncIterator() -> Iterator { Iterator(iterator: data.makeIterator()) }
    }
    func bytes(for request: URLRequest) async throws -> (AsyncBytes, URLResponse) {
        try await withCheckedThrowingContinuation { continuation in
            var task: URLSessionDataTask?
            task = dataTask(with: request) { data, response, error in
                if let error { continuation.resume(throwing: error); return }
                guard let data, let response, let task else { continuation.resume(throwing: URLError(.badServerResponse)); return }
                continuation.resume(returning: (AsyncBytes(data: data, task: task), response))
            }
            task?.resume()
        }
    }
}

@main private struct QueryClient {
    static func main() async throws {
        let client = ApiClient(baseURL: CommandLine.arguments[1])
        let query = testQueryInput(limit: 0, unread: false, text: "a +&雪", ids: "a,b", maybe: nil, extra: "ignored")
        let first = try await client.list(queryPath: "full", query: query)
        precondition(first.kind == "listed")
        let blank = try await client.list(queryPath: "blank", query: testQueryInput(limit: 0, unread: false, text: "", ids: "a,b", maybe: nil, extra: "ignored"))
        precondition(blank.kind == "listed")
        let empty = try await client.list(queryPath: "empty", query: testQueryInput(limit: nil, unread: nil, text: nil, ids: nil, maybe: nil, extra: "ignored"))
        precondition(empty.kind == "listed")
        let updated = try await client.update(query: query, req: testSDKEvent(kind: "request", text: nil), headers: ["X-Test": "expected"])
        precondition(updated.kind == "listed")
        for stream in [client.events(query: query), client.eventsReconnecting(query: query), client.namedEvents(query: query), client.namedEventsReconnecting(query: query)] {
            var iterator = stream.makeAsyncIterator()
            let event = try await iterator.next()
            precondition(event?.kind == "listed")
        }
    }
}
