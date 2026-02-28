provider "aws" {
  region = "ap-south-1"
}

# 1. Fetch the VPC automatically created by eksctl
data "aws_vpc" "eks_vpc" {
  tags = {
    "Name" = "eksctl-mini-lambda-prod-cluster/VPC"
  }
}

# 2. Fetch the Private Subnets where managed services should reside
data "aws_subnets" "private" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.eks_vpc.id]
  }
  tags = {
    "kubernetes.io/role/internal-elb" = "1"
  }
}

# 3. Fetch the EKS Node Security Group to allow traffic FROM the EKS pods
data "aws_security_group" "eks_nodes" {
  tags = {
    "Name" = "eksctl-mini-lambda-prod-cluster/ClusterSharedNodeSecurityGroup"
  }
}

# 4. Create a Security Group for RDS and ElastiCache that trusts the EKS Nodes
resource "aws_security_group" "managed_services" {
  name_prefix = "mini-lambda-managed-sg-"
  vpc_id      = data.aws_vpc.eks_vpc.id

  # Allow Postgres from EKS
  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [data.aws_security_group.eks_nodes.id]
  }

  # Allow Redis from EKS
  ingress {
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = [data.aws_security_group.eks_nodes.id]
  }
}


resource "aws_elasticache_subnet_group" "main" {
  name       = "mini-lambda-cache-subnets"
  subnet_ids = data.aws_subnets.private.ids
}


# 6. Provision Amazon ElastiCache (Redis)
resource "aws_elasticache_cluster" "redis" {
  cluster_id           = "mini-lambda-redis"
  engine               = "redis"
  node_type            = "cache.t4g.micro" # Modify for production scales
  num_cache_nodes      = 1
  port                 = 6379
  subnet_group_name    = aws_elasticache_subnet_group.main.name
  security_group_ids   = [aws_security_group.managed_services.id]
}

# 7. Provision Amazon S3 Bucket for function code and metadata
resource "aws_s3_bucket" "lambda_storage" {
  bucket_prefix = "mini-lambda-storage-"
}

output "s3_bucket_name" {
  value = aws_s3_bucket.lambda_storage.id
}

output "redis_endpoint" {
  value = aws_elasticache_cluster.redis.cache_nodes[0].address
}
