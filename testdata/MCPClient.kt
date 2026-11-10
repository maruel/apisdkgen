// Exercises generic JSON-RPC HTTP and POST SSE clients with Go-shaped fields.
package mcpcheck

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.OkHttpClient

fun main(args: Array<String>) = runBlocking {
    val http = OkHttpClient()
    val json = Json { ignoreUnknownKeys = true }
    try {
        val client = ApiClient(baseURL = args[0], httpClient = http)
        val req = json.decodeFromString<JSONRPCRequest>(java.io.File(args[1]).readText())
        val headers = mapOf("Mcp-Method" to "server/discover", "Mcp-Protocol-Version" to "2026-07-28")
        val result = client.mcp(req, headers).result
        check(result is JSONPresence.Value && result.value.jsonObject.getValue("instructions").jsonPrimitive.content == "ready")
        val event = client.stream("", "POST", json.encodeToString(req), headers) { json.decodeFromString<JSONRPCNotification>(it) }.first()
        check(event.method == "notification")
        check(client.stream("/empty-stream", "POST") { it }.first() == "empty-ok")
    } finally {
        http.dispatcher.executorService.shutdown()
        http.connectionPool.evictAll()
    }
}
