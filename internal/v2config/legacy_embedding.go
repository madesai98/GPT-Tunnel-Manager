package v2config

// EmbeddingCredentialRef is retained only so released v2 configuration and
// deprecated app APIs can be read during the local-embedding migration. The
// local embedding provider never reads this secret or sends embedding data to
// an online service.
const EmbeddingCredentialRef = "secret://embedding/openai-compatible/default"
