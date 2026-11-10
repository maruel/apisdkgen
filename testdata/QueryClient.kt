// Exercises typed URL queries independently of JSON bodies and SSE responses.
package querycheck

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking

fun main(args: Array<String>) = runBlocking {
    val http = okhttp3.OkHttpClient()
    val client = ApiClient(baseURL = args[0], httpClient = http)
    try {
        val query = testQueryInputQuery(limit = 0, unread = false, text = "a +&雪", ids = "a,b", maybe = null, extra = "ignored")
        check(client.list("full", query).kind == "listed")
        check(client.list("blank", query.copy(text = "")).kind == "listed")
        check(client.list("empty", testQueryInputQuery(extra = "ignored")).kind == "listed")
        check(client.update(query, testSDKEvent(kind = "request"), mapOf("X-Test" to "expected")).kind == "listed")
        check(client.reuse(query, testQueryInput(limit = null, unread = null, text = null, ids = null, extra = "body")).kind == "listed")
        check(client.rootList(null) == null && client.rootMap(null) == null && client.rootArray() == null)
        check(client.queryOnlyEmpty(testStructOnlyQueryQuery()).kind == "listed")
        check(client.queryOnlyValue(testStructOnlyQueryQuery(since = "x")).kind == "listed")
        check(client.rootBytesHeaders(mapOf("X-Test" to "expected"))?.contentEquals(byteArrayOf(0,1,-1)) == true)
        check(client.rootBytes(byteArrayOf(0,1,-1))?.contentEquals(byteArrayOf(0,1,-1)) == true)
        val nestedBytes = client.rootNestedBytes(listOf(byteArrayOf(0,1,-1), null))
        check(nestedBytes?.get(0)?.contentEquals(byteArrayOf(0,1,-1)) == true && nestedBytes[1] == null)
        val mappedBytes = client.rootBytesMap(mapOf("x" to byteArrayOf(0,1,-1), "empty" to null))
        check(mappedBytes?.get("x")?.contentEquals(byteArrayOf(0,1,-1)) == true && mappedBytes.containsKey("empty") && mappedBytes["empty"] == null)
        check(client.rootList(emptyList()) == emptyList<Int>() && client.rootMap(emptyMap()) == emptyMap<String, Int>())
        check(client.events(query).first().kind == "listed")
        check(client.eventsReconnecting(query).first().kind == "listed")
        check(client.namedEvents(query).first().kind == "listed")
        check(client.namedEventsReconnecting(query).first().kind == "listed")
    } finally {
        http.dispatcher.executorService.shutdown()
        http.connectionPool.evictAll()
    }
}
