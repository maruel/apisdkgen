import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonEncoder
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject

@Serializable(with = MetaCodec::class)
data class testCustomMeta(val kind: String, val identity: testMetaIdentity, val extra: Map<String, JsonElement>)
object MetaCodec : KSerializer<testCustomMeta> {
    override val descriptor: SerialDescriptor = JsonObject.serializer().descriptor
    override fun deserialize(decoder: Decoder): testCustomMeta {
        val input = decoder as JsonDecoder
        val obj = input.decodeJsonElement().jsonObject
        val identity = input.json.decodeFromJsonElement(testMetaIdentity.serializer(), obj.getValue("identity"))
        val kind = obj.getValue("kind") as? JsonPrimitive ?: throw SerializationException("kind must be a string")
        if (!kind.isString) throw SerializationException("kind must be a string")
        return testCustomMeta(kind.content, identity, obj - "identity" - "kind")
    }
    override fun serialize(encoder: Encoder, value: testCustomMeta) {
        val output = encoder as JsonEncoder
        output.encodeJsonElement(JsonObject(value.extra + ("identity" to output.json.encodeToJsonElement(testMetaIdentity.serializer(), value.identity)) + ("kind" to JsonPrimitive(value.kind))))
    }
}
